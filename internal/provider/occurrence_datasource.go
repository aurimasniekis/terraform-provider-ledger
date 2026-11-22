package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aurimasniekis/terraform-provider-ledger/internal/client"
)

type occurrenceDataSource struct{ client *client.Client }

var (
	_ datasource.DataSource                   = (*occurrenceDataSource)(nil)
	_ datasource.DataSourceWithConfigure      = (*occurrenceDataSource)(nil)
	_ datasource.DataSourceWithValidateConfig = (*occurrenceDataSource)(nil)
)

// NewOccurrenceDataSource is the constructor of the ledger_occurrence data source.
func NewOccurrenceDataSource() datasource.DataSource { return &occurrenceDataSource{} }

type occurrenceModel struct {
	ID                    types.String `tfsdk:"id"`
	CommitmentID          types.String `tfsdk:"commitment_id"`
	Month                 types.String `tfsdk:"month"`
	IncludeCancelled      types.Bool   `tfsdk:"include_cancelled"`
	DueDate               types.String `tfsdk:"due_date"`
	Amount                types.String `tfsdk:"amount"`
	Currency              types.String `tfsdk:"currency"`
	Version               types.Int64  `tfsdk:"version"`
	CancelledAt           types.String `tfsdk:"cancelled_at"`
	CancelledReason       types.String `tfsdk:"cancelled_reason"`
	Status                types.String `tfsdk:"status"`
	Timing                types.String `tfsdk:"timing"`
	PaidAmount            types.String `tfsdk:"paid_amount"`
	OutstandingAmount     types.String `tfsdk:"outstanding_amount"`
	UnconvertedCurrencies types.List   `tfsdk:"unconverted_currencies"`
	GraceUntil            types.String `tfsdk:"grace_until"`
	WithinGrace           types.Bool   `tfsdk:"within_grace"`
}

func (d *occurrenceDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_occurrence"
}

func (d *occurrenceDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Finds one dated occurrence of a commitment, which is what a deferral plan hangs off. Look it up either by its " +
			"`id`, or by `commitment_id` and the reporting `month` (a commitment has at most one occurrence in a month).\n\n" +
			"**Occurrences are derived by Ledger, not configured.** It generates them from the commitment's schedule and may rewrite an unpaid " +
			"one in place or cancel and recreate it after a terms change, so an identifier found here can stop being the one in use. " +
			"Reading a month makes Ledger materialise the commitment's occurrences through it; months more than 60 months after the current " +
			"month are refused, and a month before the commitment's start or after its end has none (the lookup fails and says so).\n\n" +
			"The payment state (status, paid and outstanding amounts) is **derived on every read** from payment evidence and dates in the " +
			"reporting time zone and can change between two reads. Amounts are exact decimal strings in the occurrence's currency; evidence in " +
			"other currencies is listed in `unconverted_currencies` and never converted or summed.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Optional: true, Computed: true, MarkdownDescription: "The occurrence's Ledger identifier. Set it to look the occurrence up by identifier.",
				Validators: []validator.String{idValidator()},
			},
			"commitment_id": schema.StringAttribute{
				Optional: true, Computed: true, MarkdownDescription: "The commitment (from the `ledger_commitment` resource or data source). With `month`, finds the occurrence.",
				Validators: []validator.String{idValidator()},
			},
			"month": schema.StringAttribute{
				Optional: true, Computed: true, MarkdownDescription: "The reporting month, `YYYY-MM`, in which the occurrence is due. With `commitment_id`, finds the occurrence.",
				Validators: []validator.String{yearMonthValidator()},
			},
			"include_cancelled": schema.BoolAttribute{
				Optional: true,
				MarkdownDescription: "With `commitment_id` and `month`: also consider an occurrence Ledger cancelled (default `false`). A cancelled occurrence " +
					"owes nothing and takes no plan.",
			},
			"due_date":           computedStr("The due date, YYYY-MM-DD."),
			"amount":             computedStr("The amount due, an exact decimal string in currency."),
			"currency":           computedStr("The ISO 4217 currency of the occurrence."),
			"version":            schema.Int64Attribute{Computed: true, MarkdownDescription: "The occurrence's version; it also moves with every payment-state change."},
			"cancelled_at":       computedStr("When Ledger cancelled the occurrence (RFC 3339, UTC), or null."),
			"cancelled_reason":   computedStr("out_of_range, terms_change or commitment_deleted, or null."),
			"status":             computedStr("The derived payment status today: paid, partially_paid, upcoming, due, overdue, not_recorded or cancelled."),
			"timing":             computedStr("upcoming, due, overdue or not_recorded for an unpaid occurrence; null when paid or cancelled."),
			"paid_amount":        computedStr("The payment evidence in the occurrence's currency, an exact decimal string (zero when none)."),
			"outstanding_amount": computedStr("What the occurrence still owes, an exact decimal string in currency; zero when paid or cancelled."),
			"unconverted_currencies": schema.ListAttribute{
				Computed: true, ElementType: types.StringType,
				MarkdownDescription: "Currencies of evidence that is not in the occurrence's currency: not counted, never converted.",
			},
			"grace_until":  computedStr("End of the grace period you recorded for an overdue occurrence, or null."),
			"within_grace": schema.BoolAttribute{Computed: true, MarkdownDescription: "True while an overdue occurrence is within its recorded grace period."},
		},
	}
}

func (d *occurrenceDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *occurrenceDataSource) ValidateConfig(ctx context.Context, req datasource.ValidateConfigRequest, resp *datasource.ValidateConfigResponse) {
	var m occurrenceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || m.ID.IsUnknown() || m.CommitmentID.IsUnknown() || m.Month.IsUnknown() {
		return
	}
	switch {
	case !m.ID.IsNull() && (!m.CommitmentID.IsNull() || !m.Month.IsNull()):
		resp.Diagnostics.AddError("Conflicting lookup", "Set either id, or commitment_id with month, not both.")
	case m.ID.IsNull() && (m.CommitmentID.IsNull() || m.Month.IsNull()):
		resp.Diagnostics.AddError("Missing lookup", "Set id, or both commitment_id and month, to say which occurrence to find.")
	case !m.ID.IsNull() && !m.IncludeCancelled.IsNull():
		resp.Diagnostics.AddError("include_cancelled does not apply", "include_cancelled only applies when looking an occurrence up by commitment_id and month.")
	}
}

func (d *occurrenceDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m occurrenceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var occ client.Occurrence
	var err error
	if !m.ID.IsNull() {
		occ, err = d.client.GetOccurrence(ctx, m.ID.ValueString())
		if client.IsNotFound(err) {
			resp.Diagnostics.AddError("Occurrence not found", fmt.Sprintf(
				"Ledger has no occurrence %s. Occurrences are derived from the commitment's schedule and may have been recreated: look it up by commitment_id and month.", m.ID.ValueString()))
			return
		}
	} else {
		var list []client.Occurrence
		list, err = d.client.ListOccurrences(ctx, m.Month.ValueString(), m.CommitmentID.ValueString(), m.IncludeCancelled.ValueBool())
		if err == nil {
			switch len(list) {
			case 1:
				occ = list[0]
			case 0:
				resp.Diagnostics.AddError("No occurrence in that month", fmt.Sprintf(
					"Commitment %s has no occurrence in %s. Its schedule may not fall due that month (a quarterly schedule skips months), the month may be before its start_date or after its end_date, or Ledger cancelled the occurrence (set include_cancelled = true to see it).",
					m.CommitmentID.ValueString(), m.Month.ValueString()))
				return
			default:
				resp.Diagnostics.AddError("More than one occurrence in that month", fmt.Sprintf(
					"Commitment %s has %d occurrences in %s, so the lookup is ambiguous. Look one up by id.", m.CommitmentID.ValueString(), len(list), m.Month.ValueString()))
				return
			}
		}
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading the occurrence failed", err.Error())
		return
	}
	cur := make([]attr.Value, 0, len(occ.Payment.UnconvertedCurrencies))
	for _, c := range occ.Payment.UnconvertedCurrencies {
		cur = append(cur, types.StringValue(c))
	}
	m.ID, m.CommitmentID, m.Month = types.StringValue(occ.ID), types.StringValue(occ.CommitmentID), types.StringValue(occ.Month)
	m.DueDate, m.Amount, m.Currency = types.StringValue(occ.DueDate), types.StringValue(occ.Amount.Amount), types.StringValue(occ.Amount.Currency)
	m.Version, m.CancelledAt, m.CancelledReason = types.Int64Value(occ.Version), strOrNull(occ.CancelledAt), strOrNull(occ.CancelledReason)
	m.Status, m.Timing = types.StringValue(occ.Payment.Status), strOrNull(occ.Payment.Timing)
	m.PaidAmount, m.OutstandingAmount = types.StringValue(occ.Payment.PaidAmount.Amount), types.StringValue(occ.Payment.OutstandingAmount.Amount)
	m.UnconvertedCurrencies = types.ListValueMust(types.StringType, cur)
	m.GraceUntil, m.WithinGrace = strOrNull(occ.Payment.GraceUntil), types.BoolValue(occ.Payment.WithinGrace)
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}
