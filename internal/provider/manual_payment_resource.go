package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"

	"github.com/aurimasniekis/terraform-provider-ledger/internal/client"
)

type manualPaymentResource struct{ client *client.Client }

var (
	_ resource.Resource                   = (*manualPaymentResource)(nil)
	_ resource.ResourceWithConfigure      = (*manualPaymentResource)(nil)
	_ resource.ResourceWithImportState    = (*manualPaymentResource)(nil)
	_ resource.ResourceWithValidateConfig = (*manualPaymentResource)(nil)
)

// NewManualPaymentResource is the constructor of ledger_manual_payment.
func NewManualPaymentResource() resource.Resource { return &manualPaymentResource{} }

func (r *manualPaymentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_manual_payment"
}

func (r *manualPaymentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A payment you made by hand and recorded against one occurrence of a commitment. It is **user data, kept " +
			"apart from bank facts**: it never changes or hides a bank transaction, and Ledger counts it as payment evidence " +
			"(the occurrence becomes paid when the payments in its currency cover its amount, partially paid below that). " +
			"Recording one makes the occurrence immutable to commitment edits and the commitment undeletable while it stands.\n\n" +
			"**The amount is an exact decimal string** and must be in the occurrence's own currency: Ledger never converts " +
			"amounts, and refuses a payment in another currency. `paid_on` must not be after today in Ledger's reporting time zone.\n\n" +
			"**Records are not edited**: a change to any attribute destroys the record and records a new one. Destroying " +
			"deletes the record (Ledger keeps a soft-deleted row for its audit trail) and the occurrence's status is derived " +
			"again from the evidence that remains. A `paid` status override stays recorded but has no effect without evidence.\n\n" +
			"The occurrence is derived by Ledger from the commitment, so its identifier is found with the `ledger_occurrence` data source. " +
			"Ledger may later replace this record by a confirmed bank match " +
			"(`replaced_by_match_id`); the record is then kept but no longer counts, and this resource shows no difference.\n\n" +
			"Import with `<occurrence_id>/<payment_id>`: `terraform import ledger_manual_payment.example <occurrence_id>/<payment_id>`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The identifier of the payment record (a ULID).",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"occurrence_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The identifier of the occurrence (one due date of a commitment) the payment is for. Changing it replaces the record.",
				Validators:          []validator.String{idValidator()},
				PlanModifiers:       replace,
			},
			"amount": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "The amount paid, an exact decimal greater than zero written as a string, for example `\"850.00\"`, in `currency`. " +
					"Changing it replaces the record.",
				Validators:    []validator.String{positiveDecimalValidator()},
				PlanModifiers: replace,
			},
			"currency": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The ISO 4217 currency code of `amount`. It must be the occurrence's currency: amounts are never converted. Changing it replaces the record.",
				Validators:          []validator.String{currencyValidator()},
				PlanModifiers:       replace,
			},
			"paid_on": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The day you paid, `YYYY-MM-DD`, not after today in Ledger's reporting time zone. Changing it replaces the record.",
				Validators:          []validator.String{dateValidator()},
				PlanModifiers:       replace,
			},
			"note": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "A free-text note, 1 to 2000 characters. Do not put secrets or account numbers here. Changing it replaces the record.",
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 2000)},
				PlanModifiers:       replace,
			},
			"version": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "The optimistic-concurrency version of the record (the ETag). A delete is sent against the version Terraform last read.",
			},
			"replaced_by_match_id": schema.StringAttribute{
				Computed: true,
				MarkdownDescription: "The identifier of the confirmed bank payment match that now stands in for this record, or null. " +
					"Set by Ledger when you confirm a bank payment as the payment you recorded; read-only.",
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "When it was recorded (RFC 3339, UTC).",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"updated_at": schema.StringAttribute{Computed: true, MarkdownDescription: "When it last changed (RFC 3339, UTC)."},
		},
	}
}

func (r *manualPaymentResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (r *manualPaymentResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m manualPaymentModel
	if d := req.Config.Get(ctx, &m); d.HasError() {
		return
	}
	resp.Diagnostics.Append(validateManualPayment(m)...)
}

func (r *manualPaymentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan manualPaymentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p, err := r.client.RecordManualPayment(ctx, plan.OccurrenceID.ValueString(), plan.input())
	if err != nil {
		resp.Diagnostics.AddError("Recording the manual payment failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, manualPaymentFromAPI(p))...)
}

func (r *manualPaymentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var st manualPaymentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p, err := r.client.GetManualPayment(ctx, st.OccurrenceID.ValueString(), st.ID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Reading the manual payment failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, manualPaymentFromAPI(p))...)
}

// Update is never reached: every writable attribute replaces the resource.
func (r *manualPaymentResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("A manual payment cannot be changed",
		"Ledger does not edit payment records; every attribute change replaces the record. This is a provider bug.")
}

func (r *manualPaymentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var st manualPaymentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteManualPayment(ctx, st.OccurrenceID.ValueString(), st.ID.ValueString(), st.Version.ValueInt64())
	switch {
	case err == nil || client.IsNotFound(err):
	case client.IsVersionConflict(err):
		resp.Diagnostics.AddError("Deleting the manual payment failed", versionConflictDetail(err))
	default:
		resp.Diagnostics.AddError("Deleting the manual payment failed", err.Error())
	}
}

func (r *manualPaymentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts, err := splitImportID(req.ID, 2, "<occurrence_id>/<payment_id>")
	if err != nil {
		resp.Diagnostics.AddError("Invalid import identifier", err.Error())
		return
	}
	if !client.ValidID(parts[0]) || !client.ValidID(parts[1]) {
		resp.Diagnostics.AddError("Invalid import identifier",
			fmt.Sprintf("Expected two Ledger identifiers (26-character upper-case ULIDs), got %q.", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("occurrence_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
}
