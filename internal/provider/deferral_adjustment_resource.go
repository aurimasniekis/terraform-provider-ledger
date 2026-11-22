package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aurimasniekis/terraform-provider-ledger/internal/client"
)

type deferralAdjustmentResource struct{ client *client.Client }

var (
	_ resource.Resource                = (*deferralAdjustmentResource)(nil)
	_ resource.ResourceWithConfigure   = (*deferralAdjustmentResource)(nil)
	_ resource.ResourceWithImportState = (*deferralAdjustmentResource)(nil)
)

// NewDeferralAdjustmentResource is the constructor of ledger_deferral_plan_adjustment.
func NewDeferralAdjustmentResource() resource.Resource { return &deferralAdjustmentResource{} }

type deferralAdjustmentResourceModel struct {
	ID             types.String `tfsdk:"id"`
	DeferralPlanID types.String `tfsdk:"deferral_plan_id"`
	Kind           types.String `tfsdk:"kind"`
	Amount         types.String `tfsdk:"amount"`
	Currency       types.String `tfsdk:"currency"`
	Note           types.String `tfsdk:"note"`
	Paid           types.Bool   `tfsdk:"paid"`
	CreatedAt      types.String `tfsdk:"created_at"`
	PaidAt         types.String `tfsdk:"paid_at"`
	PaidVia        types.String `tfsdk:"paid_via"`
}

func deferralAdjustmentResourceFromAPI(planID string, a client.DeferralAdjustment) deferralAdjustmentResourceModel {
	return deferralAdjustmentResourceModel{
		ID: types.StringValue(a.ID), DeferralPlanID: types.StringValue(planID), Kind: types.StringValue(a.Kind),
		Amount: types.StringValue(a.Amount.Amount), Currency: types.StringValue(a.Amount.Currency), Note: strOrNull(a.Note),
		Paid: types.BoolValue(a.PaidAt != nil), CreatedAt: types.StringValue(a.CreatedAt), PaidAt: strOrNull(a.PaidAt), PaidVia: actorVia(a.PaidBy),
	}
}

func findAdjustment(p client.DeferralPlan, id string) *client.DeferralAdjustment {
	for i := range p.Adjustments {
		if p.Adjustments[i].ID == id {
			return &p.Adjustments[i]
		}
	}
	return nil
}

func (r *deferralAdjustmentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_deferral_plan_adjustment"
}

func (r *deferralAdjustmentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	useState := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "An explicit fee, interest or other charge you record on a deferral plan. A plan never adds one by itself. " +
			"The amount is an exact decimal string in the plan's currency (nothing is converted) and it never changes the occurrence's " +
			"amount or what is outstanding.\n\n" +
			"**A recorded charge stays planned until it is marked paid or removed**: it counts in the plan's month under its own currency " +
			"whether or not the occurrence itself is paid. `paid` is your own marker, reversible, and **not payment evidence**: marking a " +
			"charge paid does not touch the occurrence, and paying the occurrence does not touch the charge.\n\n" +
			"Only `paid` can change in place; changing the kind, amount, currency or note replaces the adjustment (the old one is removed " +
			"and kept by Ledger as history). If creating with `paid = true` fails at the mark-paid call, the adjustment is tainted and the next apply replaces it; the first stays in Ledger as removed history. **Destroy removes the adjustment** (a paid one too); Ledger keeps it as history. Ledger refuses " +
			"to undo a plan while an adjustment is still planned, so destroying the plan's configuration removes these first. An adjustment that is " +
			"found removed outside Terraform, or whose plan was undone, is removed from state on refresh.\n\n" +
			"Import with `<plan id>/<adjustment id>`: `terraform import ledger_deferral_plan_adjustment.example <plan id>/<adjustment id>`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, MarkdownDescription: "The adjustment's Ledger identifier (a ULID).", PlanModifiers: useState},
			"deferral_plan_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The active plan the charge is recorded on. Changing it replaces the adjustment.",
				Validators:          []validator.String{idValidator()},
				PlanModifiers:       replace,
			},
			"kind": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "`fee`, `interest` or `other`.",
				Validators:          []validator.String{stringvalidator.OneOf(deferralAdjustmentKinds...)},
				PlanModifiers:       replace,
			},
			"amount": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "The charge, an exact decimal greater than zero written as a string, for example `\"5.00\"`. Its scale is kept. " +
					"Never write it as a number.",
				Validators:    []validator.String{positiveDecimalValidator()},
				PlanModifiers: replace,
			},
			"currency": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The ISO 4217 currency code of `amount`. It must be the plan's currency (the plan's `currency` attribute); Ledger refuses otherwise.",
				Validators:          []validator.String{currencyValidator()},
				PlanModifiers:       replace,
			},
			"note": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "A note, 1 to 500 characters. Do not put secrets or account numbers here.",
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 500)},
				PlanModifiers:       replace,
			},
			"paid": schema.BoolAttribute{
				Optional: true, Computed: true, Default: booldefault.StaticBool(false),
				MarkdownDescription: "Whether you marked the charge paid (default `false`). Your own record, reversible, and not payment evidence for the occurrence.",
			},
			"created_at": schema.StringAttribute{Computed: true, MarkdownDescription: "When it was recorded (RFC 3339, UTC).", PlanModifiers: useState},
			"paid_at":    schema.StringAttribute{Computed: true, MarkdownDescription: "When it was marked paid (RFC 3339, UTC), or null while it is planned."},
			"paid_via": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "How it was marked paid: `session`, `token`, `mcp` or `system`, or null while planned or when provenance was not recorded.",
			},
		},
	}
}

func (r *deferralAdjustmentResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (r *deferralAdjustmentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan deferralAdjustmentResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	planID := plan.DeferralPlanID.ValueString()
	defer lockPlan(planID)()
	cur, err := r.client.GetDeferralPlan(ctx, planID)
	if err != nil {
		resp.Diagnostics.AddError("Reading the deferral plan failed", err.Error())
		return
	}
	updated, err := r.client.AddDeferralAdjustment(ctx, planID, cur.Version, client.DeferralAdjustmentInput{
		Kind: plan.Kind.ValueString(), Amount: client.Money{Amount: plan.Amount.ValueString(), Currency: plan.Currency.ValueString()},
		Note: nullableString(plan.Note),
	})
	if err != nil {
		resp.Diagnostics.AddError("Recording the adjustment failed", deferralHint(err))
		return
	}
	before := map[string]bool{}
	for _, a := range cur.Adjustments {
		before[a.ID] = true
	}
	var created *client.DeferralAdjustment
	for i := range updated.Adjustments {
		if !before[updated.Adjustments[i].ID] {
			created = &updated.Adjustments[i]
		}
	}
	if created == nil {
		resp.Diagnostics.AddError("Recording the adjustment failed", "Ledger accepted the adjustment but did not return it on the plan. Check the plan with the ledger_deferral_plan data source.")
		return
	}
	if plan.Paid.ValueBool() {
		marked, err := r.client.MarkDeferralAdjustmentPaid(ctx, planID, created.ID, updated.Version)
		if err != nil {
			// The adjustment exists: keep it in state (unpaid). Returning an error taints the resource, so the next apply replaces it:
			// the first adjustment stays in Ledger as removed history and one new adjustment is recorded (never two active).
			resp.Diagnostics.Append(resp.State.Set(ctx, deferralAdjustmentResourceFromAPI(planID, *created))...)
			resp.Diagnostics.AddError("The adjustment was recorded but marking it paid failed", deferralHint(err)+" The adjustment is tainted: apply again to replace it (the first one stays in Ledger as removed history).")
			return
		}
		if a := findAdjustment(marked, created.ID); a != nil {
			created = a
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, deferralAdjustmentResourceFromAPI(planID, *created))...)
}

func (r *deferralAdjustmentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var st deferralAdjustmentResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	planID := st.DeferralPlanID.ValueString()
	p, err := r.client.GetDeferralPlan(ctx, planID)
	if err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Reading the deferral plan failed", err.Error())
		return
	}
	a := findAdjustment(p, st.ID.ValueString())
	if a == nil || a.RemovedAt != nil || p.State == "undone" {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, deferralAdjustmentResourceFromAPI(planID, *a))...)
}

// Update only changes the paid marker; every other attribute replaces the adjustment.
func (r *deferralAdjustmentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, st deferralAdjustmentResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	planID, id := st.DeferralPlanID.ValueString(), st.ID.ValueString()
	defer lockPlan(planID)()
	cur, err := r.client.GetDeferralPlan(ctx, planID)
	if err != nil {
		resp.Diagnostics.AddError("Reading the deferral plan failed", err.Error())
		return
	}
	a := findAdjustment(cur, id)
	if a == nil || a.RemovedAt != nil {
		resp.Diagnostics.AddError("The adjustment was removed outside Terraform", "Run terraform plan to refresh; the adjustment is then planned for creation again.")
		return
	}
	want, have := plan.Paid.ValueBool(), a.PaidAt != nil
	if want != have {
		var updated client.DeferralPlan
		if want {
			updated, err = r.client.MarkDeferralAdjustmentPaid(ctx, planID, id, cur.Version)
		} else {
			updated, err = r.client.UnmarkDeferralAdjustmentPaid(ctx, planID, id, cur.Version)
		}
		if err != nil {
			resp.Diagnostics.AddError("Changing the paid marker failed", deferralHint(err))
			return
		}
		if a2 := findAdjustment(updated, id); a2 != nil {
			a = a2
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, deferralAdjustmentResourceFromAPI(planID, *a))...)
}

func (r *deferralAdjustmentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var st deferralAdjustmentResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	planID, id := st.DeferralPlanID.ValueString(), st.ID.ValueString()
	defer lockPlan(planID)()
	cur, err := r.client.GetDeferralPlan(ctx, planID)
	if err != nil {
		if !client.IsNotFound(err) {
			resp.Diagnostics.AddError("Reading the deferral plan failed", err.Error())
		}
		return
	}
	if a := findAdjustment(cur, id); a == nil || a.RemovedAt != nil || cur.State == "undone" {
		return
	}
	if _, err := r.client.RemoveDeferralAdjustment(ctx, planID, id, cur.Version); err != nil {
		switch {
		case client.IsNotFound(err) || client.HasCode(err, client.CodeDeferralAdjustmentRemoved) || client.HasCode(err, client.CodeDeferralPlanUndone):
		default:
			resp.Diagnostics.AddError("Removing the adjustment failed", deferralHint(err))
		}
	}
}

func (r *deferralAdjustmentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts, err := splitImportID(req.ID, 2, "<plan id>/<adjustment id>")
	if err != nil || !client.ValidID(parts[0]) || !client.ValidID(parts[1]) {
		resp.Diagnostics.AddError("Invalid import identifier",
			fmt.Sprintf("Expected `<plan id>/<adjustment id>`, both 26-character upper-case ULIDs, got %q.", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("deferral_plan_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
}
