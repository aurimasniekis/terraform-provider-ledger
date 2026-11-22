package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aurimasniekis/terraform-provider-ledger/internal/client"
)

func computedStr(d string) schema.StringAttribute {
	return schema.StringAttribute{Computed: true, MarkdownDescription: d}
}

// deferralPlanDataSourceAttributes describes a plan read-only, live derived state included. idRequired makes id the
// lookup key (the single data source); in the list every member is computed.
func deferralPlanDataSourceAttributes(idRequired bool) map[string]schema.Attribute {
	id := schema.StringAttribute{Computed: true, MarkdownDescription: "The Ledger identifier (a ULID)."}
	if idRequired {
		id = schema.StringAttribute{Required: true, MarkdownDescription: "The Ledger identifier (a ULID) of the plan.", Validators: []validator.String{idValidator()}}
	}
	return map[string]schema.Attribute{
		"id":                       id,
		"occurrence_id":            computedStr("The occurrence the plan defers."),
		"commitment_id":            computedStr("The commitment the occurrence belongs to."),
		"plan_type":                computedStr("personal, creditor_agreed or documented_grace."),
		"state":                    computedStr("active, or undone (ended and never changing again; kept as history)."),
		"planned_date":             computedStr("The date the payment is planned, YYYY-MM-DD."),
		"planned_month":            computedStr("The month of planned_date, YYYY-MM: where the outstanding amount is planned."),
		"reason":                   computedStr("Why, or null."),
		"currency":                 computedStr("The occurrence's ISO 4217 currency. Nothing is converted."),
		"original_due_date":        computedStr("The occurrence's due date when the plan was made (history)."),
		"original_amount":          computedStr("The occurrence's amount when the plan was made, an exact decimal string in currency (history)."),
		"outstanding_when_planned": computedStr("What the occurrence still owed when the plan was made, an exact decimal string in currency (history)."),
		"occurrence_status":        computedStr("The occurrence's payment status today (paid, partially_paid, upcoming, due, overdue, not_recorded or cancelled). Derived on every read from payment evidence and dates; a plan never changes it."),
		"outstanding_amount":       computedStr("What the occurrence still owes today, an exact decimal string in currency. Derived on every read."),
		"overdue_since":            computedStr("The occurrence's current due date while it is overdue, otherwise null. A plan never moves it."),
		"grace_until":              computedStr("The planned date of an active documented_grace plan, otherwise null."),
		"within_grace": schema.BoolAttribute{
			Computed:            true,
			MarkdownDescription: "True while the occurrence is overdue and today is not after grace_until (overdue, within documented grace).",
		},
		"missed": schema.BoolAttribute{
			Computed: true,
			MarkdownDescription: "True while the plan is active, its planned date has passed and the occurrence is overdue and still owes something, " +
				"or a recorded fee is still planned. Derived on every read and not payment state.",
		},
		"missed_reason":           computedStr("principal (the occurrence still owes something; takes precedence) or fee (a recorded fee is still planned), or null when not missed."),
		"adjustment_total":        computedStr("The adjustments not removed, paid ones included, an exact decimal string in currency."),
		"unpaid_adjustment_total": computedStr("The adjustments still planned (neither removed nor marked paid), an exact decimal string in currency."),
		"adjustments": schema.ListNestedAttribute{
			Computed:            true,
			MarkdownDescription: "Every adjustment, oldest first, removed ones included (they are history).",
			NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"id":         computedStr("The adjustment's Ledger identifier."),
				"kind":       computedStr("fee, interest or other."),
				"amount":     computedStr("The charge, an exact decimal string in currency."),
				"currency":   computedStr("The ISO 4217 currency of amount."),
				"note":       computedStr("The note, or null."),
				"created_at": computedStr("When it was recorded (RFC 3339, UTC)."),
				"removed_at": computedStr("When it was removed (RFC 3339, UTC), or null. A removed adjustment does not count."),
				"paid_at":    computedStr("When the person marked it paid, or null while planned. Never payment evidence for the occurrence."),
				"paid_via":   computedStr("session, token, mcp or system, or null."),
			}},
		},
		"version":    schema.Int64Attribute{Computed: true, MarkdownDescription: "The plan's version; it moves with every write to the plan, adjustments included."},
		"created_at": computedStr("When it was created (RFC 3339, UTC)."),
		"updated_at": computedStr("When it last changed (RFC 3339, UTC)."),
		"undone_at":  computedStr("When it was undone (RFC 3339, UTC), or null while active."),
	}
}

type deferralPlanDataSource struct{ client *client.Client }

var (
	_ datasource.DataSource              = (*deferralPlanDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*deferralPlanDataSource)(nil)
)

// NewDeferralPlanDataSource is the constructor of the ledger_deferral_plan data source.
func NewDeferralPlanDataSource() datasource.DataSource { return &deferralPlanDataSource{} }

func (d *deferralPlanDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_deferral_plan"
}

func (d *deferralPlanDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads one deferral plan by identifier, with its adjustments and the occurrence's live state. A plan is your own " +
			"record and never payment: the occurrence's status, what it still owes, whether the plan date was missed and documented grace are " +
			"derived by Ledger on every read from payment evidence and dates, so they can change between two reads without any change to the plan. " +
			"Amounts are exact decimal strings and are never converted between currencies.",
		Attributes: deferralPlanDataSourceAttributes(true),
	}
}

func (d *deferralPlanDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *deferralPlanDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var id types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("id"), &id)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p, err := d.client.GetDeferralPlan(ctx, id.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.Diagnostics.AddError("Deferral plan not found", fmt.Sprintf("Ledger has no deferral plan %s.", id.ValueString()))
			return
		}
		resp.Diagnostics.AddError("Reading the deferral plan failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, deferralPlanFromAPI(p))...)
}

type deferralPlansDataSource struct{ client *client.Client }

var (
	_ datasource.DataSource              = (*deferralPlansDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*deferralPlansDataSource)(nil)
)

// NewDeferralPlansDataSource is the constructor of the ledger_deferral_plans data source.
func NewDeferralPlansDataSource() datasource.DataSource { return &deferralPlansDataSource{} }

type deferralPlansModel struct {
	OccurrenceID types.String        `tfsdk:"occurrence_id"`
	CommitmentID types.String        `tfsdk:"commitment_id"`
	State        types.String        `tfsdk:"state"`
	PlannedMonth types.String        `tfsdk:"planned_month"`
	Items        []deferralPlanModel `tfsdk:"items"`
}

func (d *deferralPlansDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_deferral_plans"
}

func (d *deferralPlansDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists deferral plans, active and undone, optionally filtered, in identifier (creation) order. Every page is read. " +
			"The live state of each plan is derived by Ledger on every read; see the `ledger_deferral_plan` data source.",
		Attributes: map[string]schema.Attribute{
			"occurrence_id": schema.StringAttribute{Optional: true, MarkdownDescription: "Only plans of this occurrence.", Validators: []validator.String{idValidator()}},
			"commitment_id": schema.StringAttribute{Optional: true, MarkdownDescription: "Only plans of this commitment's occurrences.", Validators: []validator.String{idValidator()}},
			"state": schema.StringAttribute{
				Optional: true, MarkdownDescription: "Only plans in this state: active or undone.",
				Validators: []validator.String{stringvalidator.OneOf(deferralPlanStates...)},
			},
			"planned_month": schema.StringAttribute{
				Optional: true, MarkdownDescription: "Only plans whose planned date is in this month, YYYY-MM.",
				Validators: []validator.String{yearMonthValidator()},
			},
			"items": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "The matching plans.",
				NestedObject:        schema.NestedAttributeObject{Attributes: deferralPlanDataSourceAttributes(false)},
			},
		},
	}
}

func (d *deferralPlansDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *deferralPlansDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m deferralPlansModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	f := client.DeferralPlanFilter{
		OccurrenceID: m.OccurrenceID.ValueString(), CommitmentID: m.CommitmentID.ValueString(),
		State: m.State.ValueString(), PlannedMonth: m.PlannedMonth.ValueString(),
	}
	list, err := d.client.ListDeferralPlans(ctx, f)
	if err != nil {
		resp.Diagnostics.AddError("Listing the deferral plans failed", err.Error())
		return
	}
	m.Items = make([]deferralPlanModel, 0, len(list))
	for _, p := range list {
		m.Items = append(m.Items, deferralPlanFromAPI(p))
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

// History.

type deferralHistoryDataSource struct{ client *client.Client }

var (
	_ datasource.DataSource              = (*deferralHistoryDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*deferralHistoryDataSource)(nil)
)

// NewDeferralHistoryDataSource is the constructor of the ledger_deferral_plan_history data source.
func NewDeferralHistoryDataSource() datasource.DataSource { return &deferralHistoryDataSource{} }

type deferralEventModel struct {
	Action       types.String `tfsdk:"action"`
	PlanVersion  types.Int64  `tfsdk:"plan_version"`
	PlanType     types.String `tfsdk:"plan_type"`
	PlannedDate  types.String `tfsdk:"planned_date"`
	Reason       types.String `tfsdk:"reason"`
	AdjustmentID types.String `tfsdk:"adjustment_id"`
	ByKind       types.String `tfsdk:"by_kind"`
	ByVia        types.String `tfsdk:"by_via"`
	At           types.String `tfsdk:"at"`
}

type deferralHistoryModel struct {
	DeferralPlanID types.String         `tfsdk:"deferral_plan_id"`
	Events         []deferralEventModel `tfsdk:"events"`
}

func (d *deferralHistoryDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_deferral_plan_history"
}

func (d *deferralHistoryDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads a deferral plan's append-only history, oldest first: its creation, every revision, adjustments added, removed, " +
			"marked paid or unpaid, and the undo, each with the plan's type and planned date after it, who made it and how, and the reason " +
			"recorded with it. Every page is read. History is a record of what you and others decided; it never changes an occurrence.",
		Attributes: map[string]schema.Attribute{
			"deferral_plan_id": schema.StringAttribute{Required: true, MarkdownDescription: "The plan, active or undone.", Validators: []validator.String{idValidator()}},
			"events": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "The changes, oldest first.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"action":        computedStr("created, revised, undone, adjustment_added, adjustment_removed, adjustment_paid or adjustment_unpaid."),
					"plan_version":  schema.Int64Attribute{Computed: true, MarkdownDescription: "The plan version the change produced."},
					"plan_type":     computedStr("The plan type after the change."),
					"planned_date":  computedStr("The planned date after the change, YYYY-MM-DD."),
					"reason":        computedStr("The reason recorded with the change, or null."),
					"adjustment_id": computedStr("The adjustment added, removed or marked paid or unpaid; null for other changes."),
					"by_kind":       computedStr("Who made the change: user, token or system."),
					"by_via":        computedStr("How: session, token, mcp or system; null for changes recorded before provenance was."),
					"at":            computedStr("When (RFC 3339, UTC)."),
				}},
			},
		},
	}
}

func (d *deferralHistoryDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *deferralHistoryDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m deferralHistoryModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	events, err := d.client.ListDeferralPlanHistory(ctx, m.DeferralPlanID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.Diagnostics.AddError("Deferral plan not found", fmt.Sprintf("Ledger has no deferral plan %s.", m.DeferralPlanID.ValueString()))
			return
		}
		resp.Diagnostics.AddError("Reading the deferral plan history failed", err.Error())
		return
	}
	m.Events = make([]deferralEventModel, 0, len(events))
	for _, e := range events {
		m.Events = append(m.Events, deferralEventModel{
			Action: types.StringValue(e.Action), PlanVersion: types.Int64Value(e.PlanVersion), PlanType: types.StringValue(e.PlanType),
			PlannedDate: types.StringValue(e.PlannedDate), Reason: strOrNull(e.Reason), AdjustmentID: strOrNull(e.AdjustmentID),
			ByKind: types.StringValue(e.By.Kind), ByVia: strOrNull(e.By.Via), At: types.StringValue(e.At),
		})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}
