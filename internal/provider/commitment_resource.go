package provider

import (
	"context"
	"fmt"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"

	"errors"

	"github.com/aurimasniekis/terraform-provider-ledger/internal/client"
)

type commitmentResource struct{ client *client.Client }

var (
	_ resource.Resource                   = (*commitmentResource)(nil)
	_ resource.ResourceWithConfigure      = (*commitmentResource)(nil)
	_ resource.ResourceWithImportState    = (*commitmentResource)(nil)
	_ resource.ResourceWithValidateConfig = (*commitmentResource)(nil)
)

// NewCommitmentResource is the constructor of ledger_commitment.
func NewCommitmentResource() resource.Resource { return &commitmentResource{} }

func (r *commitmentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_commitment"
}

func (r *commitmentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A recurring commitment definition: what is owed, how much, in which currency and on which schedule. " +
			"It is user data, kept apart from bank facts. The dated occurrences and their payment state (paid, upcoming, overdue) " +
			"are derived by Ledger from it and from bank transactions and manual records, and are never managed here.\n\n" +
			"**Amounts are exact decimal strings** (`\"850.00\"`), never numbers, and keep the scale written. Amounts of different " +
			"currencies are never combined or converted.\n\n" +
			"**Changing terms** (amount, currency, schedule, start or end date) reaches Ledger's unpaid occurrences from today in " +
			"the reporting time zone: overdue unpaid occurrences keep their old terms, and paid history never changes. " +
			"The API's `effective_from` is not exposed; set a future-dated change through the API if you need one.\n\n" +
			"**Destroy** deletes the definition and its unpaid occurrences. Ledger refuses while an occurrence has live payment " +
			"evidence (a manual payment or a confirmed bank match); set `end_date` to stop such a commitment instead. A " +
			"destroyed commitment that still has history is archived by Ledger and no longer appears in any read.\n\n" +
			"Import with the identifier: `terraform import ledger_commitment.example <id>`.",
		Attributes: commitmentResourceAttributes(),
	}
}

func commitmentResourceAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"id": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "The Ledger identifier (a ULID). It never changes.",
			PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		},
		"name": schema.StringAttribute{
			Required:            true,
			MarkdownDescription: "The name, 1 to 100 characters.",
			Validators:          []validator.String{stringvalidator.LengthBetween(1, 100)},
		},
		"kind": schema.StringAttribute{
			Required:            true,
			MarkdownDescription: "What the commitment pays for: `service`, `server` (servers and VPSs), `domain`, `subscription` or `debt_repayment`.",
			Validators:          []validator.String{stringvalidator.OneOf(commitmentKinds...)},
		},
		"amount": schema.StringAttribute{
			Required: true,
			MarkdownDescription: "The amount due at each occurrence, an exact decimal greater than zero written as a string, " +
				"for example `\"850.00\"`. Its scale is kept. Never write it as a number.",
			Validators: []validator.String{positiveDecimalValidator()},
		},
		"currency": schema.StringAttribute{
			Required:            true,
			MarkdownDescription: "The ISO 4217 currency code of `amount` and `match_tolerance`, three upper-case letters.",
			Validators:          []validator.String{currencyValidator()},
		},
		"schedule": schema.SingleNestedAttribute{
			Required: true,
			MarkdownDescription: "When it falls due. A day past the end of a month falls on that month's last day, and an annual " +
				"February 29 falls on February 28 in years that are not leap years. A monthly schedule counts its interval from " +
				"the month of `start_date`.",
			Attributes: map[string]schema.Attribute{
				"frequency": schema.StringAttribute{
					Required:            true,
					MarkdownDescription: "`monthly` or `annual`.",
					Validators:          []validator.String{stringvalidator.OneOf(scheduleFrequencies...)},
				},
				"interval": schema.Int64Attribute{
					Optional:            true,
					MarkdownDescription: "Monthly only, required there: months between due dates, 1 to 120 (1 = every month, 3 = quarterly).",
					Validators:          []validator.Int64{intBetween(1, 120)},
				},
				"month": schema.Int64Attribute{
					Optional:            true,
					MarkdownDescription: "Annual only, required there: the month, 1 to 12.",
					Validators:          []validator.Int64{intBetween(1, 12)},
				},
				"day": schema.Int64Attribute{
					Required:            true,
					MarkdownDescription: "The day of the month, 1 to 31. For an annual schedule it must exist in that month in a leap year.",
					Validators:          []validator.Int64{intBetween(1, 31)},
				},
			},
		},
		"start_date": schema.StringAttribute{
			Required:            true,
			MarkdownDescription: "The first day the commitment applies, `YYYY-MM-DD`, not before 1900-01-01.",
			Validators:          []validator.String{dateValidator()},
		},
		"end_date": schema.StringAttribute{
			Optional:            true,
			MarkdownDescription: "The last day the commitment applies (inclusive), `YYYY-MM-DD`, not before `start_date`. Omit for open-ended.",
			Validators:          []validator.String{dateValidator()},
		},
		"priority": schema.StringAttribute{
			Optional:            true,
			MarkdownDescription: "How important paying on time is: `essential`, `important` or `flexible`. Omit when not prioritised.",
			Validators:          []validator.String{stringvalidator.OneOf(commitmentPriorities...)},
		},
		"notes": schema.StringAttribute{
			Optional:            true,
			MarkdownDescription: "Free-text notes, 1 to 2000 characters. Do not put secrets or account numbers here.",
			Validators:          []validator.String{stringvalidator.LengthBetween(1, 2000)},
		},
		"match_tolerance": schema.StringAttribute{
			Optional: true,
			MarkdownDescription: "How far a booked bank payment may fall short of `amount`, in `currency`, and still be confirmed " +
				"automatically by Ledger's matching (an exact decimal string of zero or more). Omit for none: only the exact amount " +
				"then confirms automatically. A payment above `amount` is never confirmed automatically.",
			Validators: []validator.String{nonNegDecimalValidator()},
		},
		"leeway": schema.SingleNestedAttribute{
			Optional: true,
			MarkdownDescription: "What you know about the grace the creditor allows after a due date. Entered by you only: never " +
				"inferred from payment history, bank data or a model, with no default length. It only annotates an overdue occurrence; " +
				"it never moves a due date or hides an obligation. Omitting the block records nothing, which is not the same as " +
				"`kind = \"unknown\"` (you do not know) or `kind = \"none\"` (you state there is no grace).",
			Attributes: map[string]schema.Attribute{
				"kind": schema.StringAttribute{
					Required:            true,
					MarkdownDescription: "`unknown`, `none`, `period` (days after each due date) or `deadline` (a fixed date).",
					Validators:          []validator.String{stringvalidator.OneOf(leewayKinds...)},
				},
				"days": schema.Int64Attribute{
					Optional:            true,
					MarkdownDescription: "Required for `period` (1 to 365), not allowed otherwise.",
					Validators:          []validator.Int64{intBetween(1, 365)},
				},
				"until": schema.StringAttribute{
					Optional:            true,
					MarkdownDescription: "Required for `deadline` (`YYYY-MM-DD`): occurrences due on or before it are covered. Not allowed otherwise.",
					Validators:          []validator.String{dateValidator()},
				},
				"conditions": schema.StringAttribute{
					Optional:            true,
					MarkdownDescription: "What the grace depends on, as you know it (1 to 500 characters).",
					Validators:          []validator.String{stringvalidator.LengthBetween(1, 500)},
				},
				"consequences": schema.StringAttribute{
					Optional:            true,
					MarkdownDescription: "Fees or other consequences of paying late that you know of (1 to 500 characters). Free text; it is not counted in any total.",
					Validators:          []validator.String{stringvalidator.LengthBetween(1, 500)},
				},
				"source": schema.StringAttribute{
					Optional:            true,
					MarkdownDescription: "Where the information comes from, such as a contract clause or a letter (1 to 200 characters).",
					Validators:          []validator.String{stringvalidator.LengthBetween(1, 200)},
				},
			},
		},
		"version": schema.Int64Attribute{
			Computed:            true,
			MarkdownDescription: "The optimistic-concurrency version (the ETag). Changes are sent against the version Terraform last read; a stale one fails instead of overwriting.",
		},
		"created_at": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "When it was created (RFC 3339, UTC).",
			PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		},
		"updated_at": schema.StringAttribute{Computed: true, MarkdownDescription: "When it last changed (RFC 3339, UTC)."},
	}
}

func (r *commitmentResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (r *commitmentResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m commitmentModel
	// An unknown block (for example computed by another resource) cannot be read into the model: nothing to check yet.
	if d := req.Config.Get(ctx, &m); d.HasError() {
		return
	}
	resp.Diagnostics.Append(validateCommitment(m)...)
}

func (r *commitmentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan commitmentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c, err := r.client.CreateCommitment(ctx, plan.input())
	if err != nil {
		resp.Diagnostics.AddError("Creating the commitment failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, commitmentFromAPI(c))...)
}

func (r *commitmentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var st commitmentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c, err := r.client.GetCommitment(ctx, st.ID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Reading the commitment failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, commitmentFromAPI(c))...)
}

func (r *commitmentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, st commitmentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c, err := r.client.UpdateCommitment(ctx, st.ID.ValueString(), st.Version.ValueInt64(), plan.input())
	if err != nil {
		detail := err.Error()
		if client.IsVersionConflict(err) {
			detail = versionConflictDetail(err)
		}
		resp.Diagnostics.AddError("Updating the commitment failed", detail)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, commitmentFromAPI(c))...)
}

func (r *commitmentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var st commitmentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteCommitment(ctx, st.ID.ValueString(), st.Version.ValueInt64())
	var ae *client.APIError
	switch {
	case err == nil || client.IsNotFound(err):
	case client.IsVersionConflict(err):
		resp.Diagnostics.AddError("Deleting the commitment failed", versionConflictDetail(err))
	case errors.As(err, &ae) && ae.Status == http.StatusConflict:
		resp.Diagnostics.AddError("The commitment has payment history",
			err.Error()+" Ledger will not delete a commitment with a manual payment or a confirmed bank match. Set end_date to stop it instead, "+
				"or remove it from the configuration with terraform state rm.")
	default:
		resp.Diagnostics.AddError("Deleting the commitment failed", err.Error())
	}
}

func (r *commitmentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !client.ValidID(req.ID) {
		resp.Diagnostics.AddError("Invalid import identifier",
			fmt.Sprintf("Expected the Ledger identifier of the commitment (a 26-character upper-case ULID), got %q.", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
