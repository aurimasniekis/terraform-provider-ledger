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

// commitmentDataSourceAttributes describes a commitment read-only. idRequired makes id the lookup key (the single data
// source); in the list every member is computed.
func commitmentDataSourceAttributes(idRequired bool) map[string]schema.Attribute {
	id := schema.StringAttribute{Computed: true, MarkdownDescription: "The Ledger identifier (a ULID)."}
	if idRequired {
		id = schema.StringAttribute{Required: true, MarkdownDescription: "The Ledger identifier (a ULID) of the commitment.", Validators: []validator.String{idValidator()}}
	}
	str := func(d string) schema.StringAttribute {
		return schema.StringAttribute{Computed: true, MarkdownDescription: d}
	}
	return map[string]schema.Attribute{
		"id":              id,
		"name":            str("The name."),
		"kind":            str("What the commitment pays for: service, server, domain, subscription or debt_repayment."),
		"amount":          str("The amount due at each occurrence, an exact decimal string with its original scale."),
		"currency":        str("The ISO 4217 currency code of amount and match_tolerance."),
		"start_date":      str("The first day the commitment applies, YYYY-MM-DD."),
		"end_date":        str("The last day it applies (inclusive), or null when open-ended."),
		"priority":        str("essential, important or flexible, or null when not prioritised."),
		"notes":           str("Free-text notes, or null."),
		"match_tolerance": str("The tolerance for automatic payment confirmation (an exact decimal string in currency), or null for none."),
		"schedule": schema.SingleNestedAttribute{
			Computed:            true,
			MarkdownDescription: "When it falls due.",
			Attributes: map[string]schema.Attribute{
				"frequency": str("monthly or annual."),
				"interval":  schema.Int64Attribute{Computed: true, MarkdownDescription: "Monthly only: months between due dates; null for annual."},
				"month":     schema.Int64Attribute{Computed: true, MarkdownDescription: "Annual only: the month; null for monthly."},
				"day":       schema.Int64Attribute{Computed: true, MarkdownDescription: "The day of the month."},
			},
		},
		"leeway": schema.SingleNestedAttribute{
			Computed:            true,
			MarkdownDescription: "What the person recorded about the creditor's grace period, or null when nothing is recorded (which differs from kind unknown or none).",
			Attributes: map[string]schema.Attribute{
				"kind":         str("unknown, none, period or deadline."),
				"days":         schema.Int64Attribute{Computed: true, MarkdownDescription: "Days of grace for kind period, otherwise null."},
				"until":        str("The deadline for kind deadline, otherwise null."),
				"conditions":   str("What the grace depends on, or null."),
				"consequences": str("Known consequences of paying late, or null."),
				"source":       str("Where the information comes from, or null."),
			},
		},
		"version":    schema.Int64Attribute{Computed: true, MarkdownDescription: "The optimistic-concurrency version."},
		"created_at": str("When it was created (RFC 3339, UTC)."),
		"updated_at": str("When it last changed (RFC 3339, UTC)."),
	}
}

type commitmentDataSource struct{ client *client.Client }

var (
	_ datasource.DataSource              = (*commitmentDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*commitmentDataSource)(nil)
)

// NewCommitmentDataSource is the constructor of the ledger_commitment data source.
func NewCommitmentDataSource() datasource.DataSource { return &commitmentDataSource{} }

func (d *commitmentDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_commitment"
}

func (d *commitmentDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads one commitment definition by identifier. Occurrences and payment state are not part of it. " +
			"Amounts are exact decimal strings.",
		Attributes: commitmentDataSourceAttributes(true),
	}
}

func (d *commitmentDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *commitmentDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var id types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("id"), &id)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c, err := d.client.GetCommitment(ctx, id.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.Diagnostics.AddError("Commitment not found", fmt.Sprintf("Ledger has no commitment %s.", id.ValueString()))
			return
		}
		resp.Diagnostics.AddError("Reading the commitment failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, commitmentFromAPI(c))...)
}

type commitmentsDataSource struct{ client *client.Client }

var (
	_ datasource.DataSource              = (*commitmentsDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*commitmentsDataSource)(nil)
)

// NewCommitmentsDataSource is the constructor of the ledger_commitments data source.
func NewCommitmentsDataSource() datasource.DataSource { return &commitmentsDataSource{} }

type commitmentsModel struct {
	Kind     types.String      `tfsdk:"kind"`
	Priority types.String      `tfsdk:"priority"`
	Currency types.String      `tfsdk:"currency"`
	Items    []commitmentModel `tfsdk:"items"`
}

func (d *commitmentsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_commitments"
}

func (d *commitmentsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists commitment definitions, optionally filtered, in identifier order. Every page is read.",
		Attributes: map[string]schema.Attribute{
			"kind": schema.StringAttribute{
				Optional: true, MarkdownDescription: "Only commitments of this kind.",
				Validators: []validator.String{stringvalidator.OneOf(commitmentKinds...)},
			},
			"priority": schema.StringAttribute{
				Optional: true, MarkdownDescription: "Only commitments with this priority.",
				Validators: []validator.String{stringvalidator.OneOf(commitmentPriorities...)},
			},
			"currency": schema.StringAttribute{
				Optional: true, MarkdownDescription: "Only commitments in this currency.",
				Validators: []validator.String{currencyValidator()},
			},
			"items": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "The matching commitments.",
				NestedObject:        schema.NestedAttributeObject{Attributes: commitmentDataSourceAttributes(false)},
			},
		},
	}
}

func (d *commitmentsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *commitmentsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m commitmentsModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var f client.CommitmentFilter
	if v := nullableString(m.Kind); v != nil {
		f.Kinds = []string{*v}
	}
	if v := nullableString(m.Priority); v != nil {
		f.Priorities = []string{*v}
	}
	if v := nullableString(m.Currency); v != nil {
		f.Currencies = []string{*v}
	}
	list, err := d.client.ListCommitments(ctx, f)
	if err != nil {
		resp.Diagnostics.AddError("Listing the commitments failed", err.Error())
		return
	}
	m.Items = make([]commitmentModel, 0, len(list))
	for _, c := range list {
		m.Items = append(m.Items, commitmentFromAPI(c))
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}
