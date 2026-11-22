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
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aurimasniekis/terraform-provider-ledger/internal/client"
)

type deferralPlanResource struct{ client *client.Client }

var (
	_ resource.Resource                   = (*deferralPlanResource)(nil)
	_ resource.ResourceWithConfigure      = (*deferralPlanResource)(nil)
	_ resource.ResourceWithImportState    = (*deferralPlanResource)(nil)
	_ resource.ResourceWithValidateConfig = (*deferralPlanResource)(nil)
)

// NewDeferralPlanResource is the constructor of ledger_deferral_plan.
func NewDeferralPlanResource() resource.Resource { return &deferralPlanResource{} }

// deferralPlanResourceModel is the part of a plan Terraform manages, plus the immutable history Ledger fixed when the
// plan was made. The live derived state (the occurrence's status, what is still owed, missed, grace) is deliberately not
// here: it changes without any change to the plan and would show as drift. The ledger_deferral_plan data source has it.
type deferralPlanResourceModel struct {
	ID                     types.String `tfsdk:"id"`
	OccurrenceID           types.String `tfsdk:"occurrence_id"`
	PlanType               types.String `tfsdk:"plan_type"`
	PlannedDate            types.String `tfsdk:"planned_date"`
	Reason                 types.String `tfsdk:"reason"`
	CommitmentID           types.String `tfsdk:"commitment_id"`
	State                  types.String `tfsdk:"state"`
	PlannedMonth           types.String `tfsdk:"planned_month"`
	Currency               types.String `tfsdk:"currency"`
	OriginalDueDate        types.String `tfsdk:"original_due_date"`
	OriginalAmount         types.String `tfsdk:"original_amount"`
	OutstandingWhenPlanned types.String `tfsdk:"outstanding_when_planned"`
	CreatedAt              types.String `tfsdk:"created_at"`
}

func deferralPlanResourceFromAPI(p client.DeferralPlan) deferralPlanResourceModel {
	return deferralPlanResourceModel{
		ID: types.StringValue(p.ID), OccurrenceID: types.StringValue(p.OccurrenceID), PlanType: types.StringValue(p.PlanType),
		PlannedDate: types.StringValue(p.PlannedDate), Reason: strOrNull(p.Reason), CommitmentID: types.StringValue(p.CommitmentID),
		State: types.StringValue(p.State), PlannedMonth: types.StringValue(p.PlannedMonth), Currency: types.StringValue(p.OriginalAmount.Currency),
		OriginalDueDate: types.StringValue(p.OriginalDueDate), OriginalAmount: types.StringValue(p.OriginalAmount.Amount),
		OutstandingWhenPlanned: types.StringValue(p.OutstandingWhenPlanned.Amount),
		CreatedAt:              types.StringValue(p.CreatedAt),
	}
}

func (m deferralPlanResourceModel) input() client.DeferralPlanInput {
	return client.DeferralPlanInput{
		OccurrenceID: m.OccurrenceID.ValueString(), PlanType: m.PlanType.ValueString(),
		PlannedDate: m.PlannedDate.ValueString(), Reason: nullableString(m.Reason),
	}
}

func (r *deferralPlanResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_deferral_plan"
}

func (r *deferralPlanResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	useState := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Your plan to pay an outstanding occurrence of a commitment on a later date. **A plan is not payment, forgiveness " +
			"or a creditor agreement** (unless `plan_type` records that one exists): it never changes the occurrence's due date, " +
			"amount, payment evidence or status. A deferred occurrence stays outstanding and, past its original due date, overdue; the " +
			"original due date, the original amount and what was still owed when you planned stay visible beside the new date. It is " +
			"user data, kept apart from bank facts.\n\n" +
			"The plan hangs off one **occurrence**. Ledger derives occurrences from the commitment's schedule and may rewrite or " +
			"cancel and recreate them (for example after a schedule change), so find the occurrence with the `ledger_occurrence` " +
			"data source. `occurrence_id` cannot change: a different occurrence replaces the plan.\n\n" +
			"**Only an occurrence that still owes something can be deferred**, it may have one active plan at a time, and the planned " +
			"date must be after its due date and within Ledger's materialisation horizon. Ledger refuses otherwise and the error says why.\n\n" +
			"**Destroy undoes the plan.** The occurrence is planned in its own month again; the plan and its history are kept by Ledger " +
			"(state `undone`) and never change again, and the occurrence can be deferred anew. Ledger refuses the undo while a fee " +
			"or interest adjustment is still planned: manage fees with `ledger_deferral_plan_adjustment`, which Terraform destroys first, " +
			"and which removes them. A plan found undone outside Terraform is removed from state on refresh.\n\n" +
			"Fees and interest are never added by a plan. The live state of a plan (the occurrence's status, what is still owed, " +
			"whether the plan date was missed, documented grace) changes without any change to the plan, so it is not an attribute here: " +
			"read it with the `ledger_deferral_plan` data source.\n\n" +
			"Import with the plan's identifier: `terraform import ledger_deferral_plan.example <id>`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, MarkdownDescription: "The Ledger identifier (a ULID). It never changes.", PlanModifiers: useState},
			"occurrence_id": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "The occurrence to defer, from the `ledger_occurrence` data source. Changing it replaces the plan " +
					"(the old one is undone).",
				Validators:    []validator.String{idValidator()},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"plan_type": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "On what authority the payment is planned later: `personal` (your own plan, nothing agreed), " +
					"`creditor_agreed` (a later date the creditor agreed to) or `documented_grace` (a grace period the creditor documents; " +
					"the planned date is its last day). The occurrence is overdue from its original due date in every case.",
				Validators: []validator.String{stringvalidator.OneOf(deferralPlanTypes...)},
			},
			"planned_date": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "The date you plan to pay, `YYYY-MM-DD`: after the occurrence's due date and within Ledger's " +
					"materialisation horizon. A date already in the past is accepted (a plan recorded late); it is then flagged as missed " +
					"while the occurrence still owes something.",
				Validators: []validator.String{dateValidator()},
			},
			"reason": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Why, 1 to 500 characters. Do not put secrets or account numbers here.",
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 500)},
			},
			"commitment_id": schema.StringAttribute{Computed: true, MarkdownDescription: "The commitment the occurrence belongs to.", PlanModifiers: useState},
			"state":         schema.StringAttribute{Computed: true, MarkdownDescription: "`active`. A plan that is found `undone` is removed from state.", PlanModifiers: useState},
			"planned_month": schema.StringAttribute{Computed: true, MarkdownDescription: "The month of `planned_date` (`YYYY-MM`): the month the outstanding amount is planned in."},
			"currency": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The occurrence's ISO 4217 currency. Nothing is converted; fees on the plan must be in it.",
				PlanModifiers:       useState,
			},
			"original_due_date": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The occurrence's due date when the plan was made (history; it never moves).",
				PlanModifiers:       useState,
			},
			"original_amount": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The occurrence's amount when the plan was made, an exact decimal string in `currency` (history).",
				PlanModifiers:       useState,
			},
			"outstanding_when_planned": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "What the occurrence still owed when the plan was made, an exact decimal string in `currency` (history).",
				PlanModifiers:       useState,
			},
			"created_at": schema.StringAttribute{Computed: true, MarkdownDescription: "When it was created (RFC 3339, UTC).", PlanModifiers: useState},
		},
	}
}

func (r *deferralPlanResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (r *deferralPlanResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var d types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("planned_date"), &d)...)
	resp.Diagnostics.Append(realDate(d, path.Root("planned_date"), "planned_date")...)
}

func (r *deferralPlanResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan deferralPlanResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p, err := r.client.CreateDeferralPlan(ctx, plan.input())
	if err != nil {
		resp.Diagnostics.AddError("Creating the deferral plan failed", deferralHint(err))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, deferralPlanResourceFromAPI(p))...)
}

func (r *deferralPlanResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var st deferralPlanResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p, err := r.client.GetDeferralPlan(ctx, st.ID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Reading the deferral plan failed", err.Error())
		return
	}
	if p.State == "undone" {
		resp.Diagnostics.AddWarning("The deferral plan was undone outside Terraform",
			fmt.Sprintf("Plan %s is undone: it ended and cannot change again. It is removed from state, so the next plan shows it as to be created.", p.ID))
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, deferralPlanResourceFromAPI(p))...)
}

func (r *deferralPlanResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, st deferralPlanResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := st.ID.ValueString()
	defer lockPlan(id)()
	cur, err := r.client.GetDeferralPlan(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Reading the deferral plan before revising it failed", err.Error())
		return
	}
	if cur.State == "undone" {
		resp.Diagnostics.AddError("The deferral plan was undone outside Terraform", deferralHint(&client.APIError{Status: 409, Code: client.CodeDeferralPlanUndone, Message: "plan " + id + " is undone"}))
		return
	}
	// The plan's version also moves with its adjustments (sibling resources in this very apply), so the version Terraform
	// last read is not the test. What matters is that the plan's own terms are still what Terraform last saw.
	if cur.PlanType != st.PlanType.ValueString() || cur.PlannedDate != st.PlannedDate.ValueString() || !sameReason(cur.Reason, st.Reason) {
		resp.Diagnostics.AddError("The deferral plan changed outside Terraform", versionConflictDetail(fmt.Errorf(
			"the plan's type, planned date or reason differ from what Terraform last read (version %d now)", cur.Version)))
		return
	}
	p, err := r.client.ReviseDeferralPlan(ctx, id, cur.Version, plan.input())
	if err != nil {
		if client.IsVersionConflict(err) {
			resp.Diagnostics.AddError("Revising the deferral plan failed", versionConflictDetail(err))
			return
		}
		resp.Diagnostics.AddError("Revising the deferral plan failed", deferralHint(err))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, deferralPlanResourceFromAPI(p))...)
}

func sameReason(api *string, tf types.String) bool {
	if api == nil || tf.IsNull() {
		return api == nil && tf.IsNull()
	}
	return *api == tf.ValueString()
}

// Delete undoes the plan. Ledger keeps the plan and its history; nothing about the occurrence or its payments changes.
func (r *deferralPlanResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var st deferralPlanResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := st.ID.ValueString()
	defer lockPlan(id)()
	// The version in state may be stale because adjustments destroyed just before this undo moved it.
	cur, err := r.client.GetDeferralPlan(ctx, id)
	if err != nil {
		if !client.IsNotFound(err) {
			resp.Diagnostics.AddError("Reading the deferral plan before undoing it failed", err.Error())
		}
		return
	}
	if cur.State == "undone" {
		return
	}
	if _, err := r.client.UndoDeferralPlan(ctx, id, cur.Version); err != nil {
		switch {
		case client.IsNotFound(err) || client.HasCode(err, client.CodeDeferralPlanUndone):
		case client.IsVersionConflict(err):
			resp.Diagnostics.AddError("Undoing the deferral plan failed", versionConflictDetail(err))
		default:
			resp.Diagnostics.AddError("Undoing the deferral plan failed", deferralHint(err))
		}
	}
}

func (r *deferralPlanResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !client.ValidID(req.ID) {
		resp.Diagnostics.AddError("Invalid import identifier",
			fmt.Sprintf("Expected the Ledger identifier of the deferral plan (a 26-character upper-case ULID), got %q.", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
