package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aurimasniekis/terraform-provider-ledger/internal/client"
)

func manualPaymentDataSourceAttributes(single bool) map[string]schema.Attribute {
	str := func(d string) schema.StringAttribute {
		return schema.StringAttribute{Computed: true, MarkdownDescription: d}
	}
	attrs := map[string]schema.Attribute{
		"id":                   str("The identifier of the payment record (a ULID)."),
		"occurrence_id":        str("The occurrence the payment is for."),
		"amount":               str("The amount paid, an exact decimal string with its original scale."),
		"currency":             str("The ISO 4217 currency code of amount."),
		"paid_on":              str("The day the payment was made, YYYY-MM-DD."),
		"note":                 str("The free-text note, or null."),
		"version":              schema.Int64Attribute{Computed: true, MarkdownDescription: "The optimistic-concurrency version of the record."},
		"replaced_by_match_id": str("The confirmed bank payment match that now stands in for this record (it is then kept but does not count), or null."),
		"created_at":           str("When it was recorded (RFC 3339, UTC)."),
		"updated_at":           str("When it last changed (RFC 3339, UTC)."),
	}
	if single {
		attrs["id"] = schema.StringAttribute{Required: true, MarkdownDescription: "The identifier of the payment record (a ULID).", Validators: []validator.String{idValidator()}}
		attrs["occurrence_id"] = schema.StringAttribute{Required: true, MarkdownDescription: "The occurrence the payment is for.", Validators: []validator.String{idValidator()}}
	}
	return attrs
}

type manualPaymentDataSource struct{ client *client.Client }

var (
	_ datasource.DataSource              = (*manualPaymentDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*manualPaymentDataSource)(nil)
)

// NewManualPaymentDataSource is the constructor of the ledger_manual_payment data source.
func NewManualPaymentDataSource() datasource.DataSource { return &manualPaymentDataSource{} }

func (d *manualPaymentDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_manual_payment"
}

func (d *manualPaymentDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads one manual payment record of an occurrence: a payment recorded by hand, which is user data and not a bank fact. Amounts are exact decimal strings.",
		Attributes:          manualPaymentDataSourceAttributes(true),
	}
}

func (d *manualPaymentDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *manualPaymentDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var occ, id types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("occurrence_id"), &occ)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("id"), &id)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p, err := d.client.GetManualPayment(ctx, occ.ValueString(), id.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.Diagnostics.AddError("Manual payment not found", fmt.Sprintf("Occurrence %s has no manual payment %s.", occ.ValueString(), id.ValueString()))
			return
		}
		resp.Diagnostics.AddError("Reading the manual payment failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, manualPaymentFromAPI(p))...)
}

type manualPaymentsDataSource struct{ client *client.Client }

var (
	_ datasource.DataSource              = (*manualPaymentsDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*manualPaymentsDataSource)(nil)
)

// NewManualPaymentsDataSource is the constructor of the ledger_manual_payments data source.
func NewManualPaymentsDataSource() datasource.DataSource { return &manualPaymentsDataSource{} }

type manualPaymentsModel struct {
	OccurrenceID types.String         `tfsdk:"occurrence_id"`
	Items        []manualPaymentModel `tfsdk:"items"`
}

func (d *manualPaymentsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_manual_payments"
}

func (d *manualPaymentsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the manual payments of one occurrence, ordered by `paid_on` and then identifier. Every page is read. " +
			"Records that a confirmed bank match now stands in for are listed with `replaced_by_match_id` set. " +
			"Amounts are exact decimal strings and are never summed or converted here.",
		Attributes: map[string]schema.Attribute{
			"occurrence_id": schema.StringAttribute{Required: true, MarkdownDescription: "The occurrence to list the payments of.", Validators: []validator.String{idValidator()}},
			"items": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "The recorded payments.",
				NestedObject:        schema.NestedAttributeObject{Attributes: manualPaymentDataSourceAttributes(false)},
			},
		},
	}
}

func (d *manualPaymentsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *manualPaymentsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m manualPaymentsModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	list, err := d.client.ListManualPayments(ctx, m.OccurrenceID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Listing the manual payments failed", err.Error())
		return
	}
	m.Items = make([]manualPaymentModel, 0, len(list))
	for _, p := range list {
		m.Items = append(m.Items, manualPaymentFromAPI(p))
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

// ---- ledger_occurrence_status_override data source ----

type statusOverrideDataSource struct{ client *client.Client }

var (
	_ datasource.DataSource              = (*statusOverrideDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*statusOverrideDataSource)(nil)
)

// NewStatusOverrideDataSource is the constructor of the ledger_occurrence_status_override data source.
func NewStatusOverrideDataSource() datasource.DataSource { return &statusOverrideDataSource{} }

type statusOverrideDSModel struct {
	OccurrenceID  types.String `tfsdk:"occurrence_id"`
	Active        types.Bool   `tfsdk:"active"`
	Status        types.String `tfsdk:"status"`
	Reason        types.String `tfsdk:"reason"`
	Applied       types.Bool   `tfsdk:"applied"`
	SetAt         types.String `tfsdk:"set_at"`
	DerivedStatus types.String `tfsdk:"derived_status"`
	PaymentStatus types.String `tfsdk:"payment_status"`
}

func (d *statusOverrideDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_occurrence_status_override"
}

func (d *statusOverrideDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	str := func(desc string) schema.StringAttribute {
		return schema.StringAttribute{Computed: true, MarkdownDescription: desc}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads whether an occurrence has an active manual status override, and what it is. The status Ledger derives from " +
			"evidence and dates stays separate (`derived_status`); the override is a user correction, not a bank fact.",
		Attributes: map[string]schema.Attribute{
			"occurrence_id":  schema.StringAttribute{Required: true, MarkdownDescription: "The occurrence to read.", Validators: []validator.String{idValidator()}},
			"active":         schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether an override is active. When false, status, reason, applied and set_at are null."},
			"status":         str("paid or unpaid, or null."),
			"reason":         str("Why the status was overridden, or null."),
			"applied":        schema.BoolAttribute{Computed: true, MarkdownDescription: "False for a paid override whose evidence has been removed (no effect); null without an override."},
			"set_at":         str("When the override was set (RFC 3339, UTC), or null."),
			"derived_status": str("The payment status derived from evidence and dates alone."),
			"payment_status": str("The occurrence's payment status after any override."),
		},
	}
}

func (d *statusOverrideDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *statusOverrideDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var id types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("occurrence_id"), &id)...)
	if resp.Diagnostics.HasError() {
		return
	}
	occ, err := d.client.GetOccurrence(ctx, id.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.Diagnostics.AddError("Occurrence not found", fmt.Sprintf("Ledger has no occurrence %s.", id.ValueString()))
			return
		}
		resp.Diagnostics.AddError("Reading the occurrence failed", err.Error())
		return
	}
	m := statusOverrideDSModel{
		OccurrenceID: id, Active: types.BoolValue(occ.Payment.Override != nil),
		Status: types.StringNull(), Reason: types.StringNull(), Applied: types.BoolNull(), SetAt: types.StringNull(),
		DerivedStatus: types.StringValue(occ.Payment.DerivedStatus), PaymentStatus: types.StringValue(occ.Payment.Status),
	}
	if ov := occ.Payment.Override; ov != nil {
		m.Status, m.Reason, m.Applied, m.SetAt = types.StringValue(ov.Status), types.StringValue(ov.Reason), types.BoolValue(ov.Applied), types.StringValue(ov.SetAt)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}
