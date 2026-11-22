package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"

	"github.com/aurimasniekis/terraform-provider-ledger/internal/client"
)

// maxOverrideUndos bounds how many history steps a destroy walks back. Terraform makes one step per change it applied.
const maxOverrideUndos = 25

type statusOverrideResource struct{ client *client.Client }

var (
	_ resource.Resource                = (*statusOverrideResource)(nil)
	_ resource.ResourceWithConfigure   = (*statusOverrideResource)(nil)
	_ resource.ResourceWithImportState = (*statusOverrideResource)(nil)
)

// NewStatusOverrideResource is the constructor of ledger_occurrence_status_override.
func NewStatusOverrideResource() resource.Resource { return &statusOverrideResource{} }

func (r *statusOverrideResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_occurrence_status_override"
}

func (r *statusOverrideResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Your manual override of one occurrence's payment status, with the reason (Ledger decision D7). It is a " +
			"**user correction kept apart from bank facts**: it never changes a bank transaction or the evidence, and the status " +
			"Ledger derives from the evidence and dates stays visible as `derived_status`.\n\n" +
			"`paid` marks the occurrence settled although the counted evidence does not cover it (for example a payment in " +
			"another currency, which is never converted). It **needs payment evidence**: without a manual payment or a confirmed " +
			"bank match Ledger refuses it with `payment_evidence_required`. `unpaid` says the evidence does not settle it, and " +
			"the status follows the dates. If the evidence is later removed, a `paid` override stays recorded with " +
			"`applied = false` and has no effect.\n\n" +
			"**One override per occurrence.** Creating refuses an occurrence that already has an active override: import it " +
			"to take it over. Changing `status` or `reason` replaces the override in place. **Destroying undoes the override " +
			"history step by step until no override is active** (Ledger's undo restores the override a change replaced), so " +
			"the derived status applies again.\n\n" +
			"The occurrence is derived by Ledger from the commitment, so its identifier is taken from the occurrences API. " +
			"Changes are sent against the occurrence's current version, which also moves with every payment-state change; a " +
			"concurrent change fails with a message to refresh instead of overwriting (D17).\n\n" +
			"Import with the occurrence identifier: `terraform import ledger_occurrence_status_override.example <occurrence_id>`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The occurrence identifier (there is at most one active override per occurrence).",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"occurrence_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The identifier of the occurrence (one due date of a commitment). Changing it replaces the resource.",
				Validators:          []validator.String{idValidator()},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"status": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "`paid` (needs payment evidence) or `unpaid`.",
				Validators:          []validator.String{stringvalidator.OneOf(overrideStatuses...)},
			},
			"reason": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Why you override the status, 1 to 500 characters. Ledger keeps it out of its audit log. Do not put secrets or account numbers here.",
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 500)},
			},
			"applied": schema.BoolAttribute{
				Computed:            true,
				MarkdownDescription: "False for a `paid` override whose payment evidence has since been removed: it is recorded but has no effect.",
			},
			"set_at": schema.StringAttribute{Computed: true, MarkdownDescription: "When the active override was set (RFC 3339, UTC)."},
			"derived_status": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The payment status derived from evidence and dates alone, before the override. Read-only.",
			},
			"payment_status": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The occurrence's payment status after the override. Read-only.",
			},
		},
	}
}

func (r *statusOverrideResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (r *statusOverrideResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan statusOverrideModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := plan.OccurrenceID.ValueString()
	occ, err := r.client.GetOccurrence(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Reading the occurrence failed", err.Error())
		return
	}
	if occ.Payment.Override != nil {
		resp.Diagnostics.AddError("The occurrence already has a status override",
			fmt.Sprintf("Occurrence %s already has an active override (%s). Import it with terraform import to manage it, "+
				"or undo it first; Terraform will not replace an override it did not create.", id, occ.Payment.Override.Status))
		return
	}
	r.put(ctx, plan, &resp.State, &resp.Diagnostics, "Setting the status override failed")
}

// versionRetries bounds how often a write is retried against a fresh occurrence version. The occurrence's version moves with
// every payment-state change, including those of other resources applied in the same run (a manual payment, another
// override), so a conflict between two Terraform operations is expected and retried; the data being written does not
// depend on the version, only the guard does.
const versionRetries = 5

func (r *statusOverrideResource) put(ctx context.Context, plan statusOverrideModel, state *tfsdk.State, diags *diag.Diagnostics, summary string) {
	id := plan.OccurrenceID.ValueString()
	for attempt := 0; ; attempt++ {
		occ, err := r.client.GetOccurrence(ctx, id)
		if err == nil {
			occ, err = r.client.SetStatusOverride(ctx, id, occ.Version, plan.Status.ValueString(), plan.Reason.ValueString())
		}
		if err != nil {
			if client.IsVersionConflict(err) && attempt < versionRetries {
				continue
			}
			detail := err.Error()
			if client.IsVersionConflict(err) {
				detail = versionConflictDetail(err)
			}
			diags.AddError(summary, detail)
			return
		}
		m, ok := statusOverrideFromAPI(occ)
		if !ok {
			diags.AddError(summary, "Ledger accepted the override but reports none active on the occurrence.")
			return
		}
		diags.Append(state.Set(ctx, m)...)
		return
	}
}

func (r *statusOverrideResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var st statusOverrideModel
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	occ, err := r.client.GetOccurrence(ctx, st.OccurrenceID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Reading the occurrence failed", err.Error())
		return
	}
	m, ok := statusOverrideFromAPI(occ)
	if !ok {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

func (r *statusOverrideResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan statusOverrideModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.put(ctx, plan, &resp.State, &resp.Diagnostics, "Changing the status override failed")
}

func (r *statusOverrideResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var st statusOverrideModel
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := st.OccurrenceID.ValueString()
	conflicts := 0
	for range maxOverrideUndos + versionRetries {
		occ, err := r.client.GetOccurrence(ctx, id)
		if err != nil {
			if !client.IsNotFound(err) {
				resp.Diagnostics.AddError("Reading the occurrence failed", err.Error())
			}
			return
		}
		if occ.Payment.Override == nil {
			return
		}
		if _, err := r.client.UndoStatusOverride(ctx, id, occ.Version); err != nil {
			switch {
			case client.IsNotFound(err):
				return
			case client.IsVersionConflict(err) && conflicts < versionRetries:
				conflicts++
				continue
			case client.IsVersionConflict(err):
				resp.Diagnostics.AddError("Undoing the status override failed", versionConflictDetail(err))
			default:
				resp.Diagnostics.AddError("Undoing the status override failed", err.Error())
			}
			return
		}
	}
	resp.Diagnostics.AddError("The status override is still active",
		fmt.Sprintf("Ledger still reports an override on occurrence %s after %d undo steps. Undo it in Ledger, or remove the resource with terraform state rm.", id, maxOverrideUndos))
}

func (r *statusOverrideResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !client.ValidID(req.ID) {
		resp.Diagnostics.AddError("Invalid import identifier",
			fmt.Sprintf("Expected the Ledger identifier of the occurrence (a 26-character upper-case ULID), got %q.", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("occurrence_id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
