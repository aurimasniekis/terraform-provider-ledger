package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aurimasniekis/terraform-provider-ledger/internal/client"
)

var (
	_ datasource.DataSource              = (*merchantDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*merchantDataSource)(nil)
	_ datasource.DataSource              = (*merchantsDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*merchantsDataSource)(nil)
)

var merchantAliasAttrTypes = map[string]attr.Type{
	"id": types.StringType, "name": types.StringType, "key": types.StringType, "origin": types.StringType, "created_at": types.StringType,
}

var merchantAttrTypes = map[string]attr.Type{
	"id": types.StringType, "display_name": types.StringType, "archived": types.BoolType, "archived_at": types.StringType,
	"merged_into": types.StringType, "version": types.Int64Type, "created_at": types.StringType, "updated_at": types.StringType,
	"aliases": types.ListType{ElemType: types.ObjectType{AttrTypes: merchantAliasAttrTypes}},
}

func merchantAliasesValue(m client.Merchant) (types.List, diag.Diagnostics) {
	vals := make([]attr.Value, 0, len(m.Aliases))
	var diags diag.Diagnostics
	for _, a := range m.Aliases {
		v, d := types.ObjectValue(merchantAliasAttrTypes, map[string]attr.Value{"id": types.StringValue(a.ID), "name": types.StringValue(a.Name),
			"key": types.StringValue(a.Key), "origin": types.StringValue(a.Origin), "created_at": types.StringValue(a.CreatedAt)})
		diags.Append(d...)
		vals = append(vals, v)
	}
	l, d := types.ListValue(types.ObjectType{AttrTypes: merchantAliasAttrTypes}, vals)
	diags.Append(d...)
	return l, diags
}

func merchantValue(m client.Merchant) (attr.Value, diag.Diagnostics) {
	aliases, diags := merchantAliasesValue(m)
	v, d := types.ObjectValue(merchantAttrTypes, map[string]attr.Value{
		"id": types.StringValue(m.ID), "display_name": types.StringValue(m.DisplayName), "archived": types.BoolValue(m.Archived),
		"archived_at": strOrNull(m.ArchivedAt), "merged_into": strOrNull(m.MergedInto), "version": types.Int64Value(m.Version),
		"created_at": types.StringValue(m.CreatedAt), "updated_at": types.StringValue(m.UpdatedAt), "aliases": aliases})
	diags.Append(d...)
	return v, diags
}

func merchantItemSchema() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"id":           schema.StringAttribute{Computed: true, MarkdownDescription: "The Ledger identifier."},
		"display_name": schema.StringAttribute{Computed: true, MarkdownDescription: "The name shown for the merchant."},
		"archived":     schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether it is archived (a merged merchant is archived)."},
		"archived_at":  schema.StringAttribute{Computed: true, MarkdownDescription: "When it was archived (RFC 3339, UTC), or null."},
		"merged_into":  schema.StringAttribute{Computed: true, MarkdownDescription: "The identifier of the merchant it was merged into, or null."},
		"version":      schema.Int64Attribute{Computed: true, MarkdownDescription: "The optimistic-concurrency version."},
		"created_at":   schema.StringAttribute{Computed: true, MarkdownDescription: "When it was created (RFC 3339, UTC)."},
		"updated_at":   schema.StringAttribute{Computed: true, MarkdownDescription: "When it last changed (RFC 3339, UTC)."},
		"aliases": schema.ListNestedAttribute{Computed: true, MarkdownDescription: "The alias names transactions are matched by.",
			NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"id":         schema.StringAttribute{Computed: true, MarkdownDescription: "The alias identifier."},
				"name":       schema.StringAttribute{Computed: true, MarkdownDescription: "The alias name."},
				"key":        schema.StringAttribute{Computed: true, MarkdownDescription: "The name as matched: NFC, white space collapsed, lower case."},
				"origin":     schema.StringAttribute{Computed: true, MarkdownDescription: "`user`, `decision` or `upgrade`."},
				"created_at": schema.StringAttribute{Computed: true, MarkdownDescription: "When it was added (RFC 3339, UTC)."},
			}}},
	}
}

// ---- ledger_merchant ----

type merchantDataSource struct{ client *client.Client }

// NewMerchantDataSource is the data source constructor.
func NewMerchantDataSource() datasource.DataSource { return &merchantDataSource{} }

func (d *merchantDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_merchant"
}

func (d *merchantDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := merchantItemSchema()
	attrs["id"] = schema.StringAttribute{Optional: true, Computed: true,
		MarkdownDescription: "The Ledger identifier. Set `id` or `display_name`, not both.",
		Validators:          []validator.String{idValidator(), stringvalidator.ExactlyOneOf(path.MatchRoot("id"), path.MatchRoot("display_name"))}}
	attrs["display_name"] = schema.StringAttribute{Optional: true, Computed: true,
		MarkdownDescription: "The display name, matched ignoring case; it is returned as written. Archived and merged merchants are found too. Set `id` or `display_name`, not both. " +
			"An alias that is not the display name is not looked up here; use `id`."}
	resp.Schema = schema.Schema{MarkdownDescription: "Reads one Ledger merchant by identifier or by display name. " + merchantGrantNote, Attributes: attrs}
}

func (d *merchantDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *merchantDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var id, name types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("id"), &id)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("display_name"), &name)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var found client.Merchant
	if !id.IsNull() {
		m, err := d.client.GetMerchant(ctx, id.ValueString())
		if err != nil {
			if client.IsNotFound(err) {
				resp.Diagnostics.AddAttributeError(path.Root("id"), "No such merchant", "Ledger has no merchant with that identifier.")
				return
			}
			resp.Diagnostics.AddError("Reading the merchant failed", err.Error())
			return
		}
		found = m
	} else {
		items, err := d.client.ListMerchants(ctx, true)
		if err != nil {
			resp.Diagnostics.AddError("Looking up the merchant failed", err.Error())
			return
		}
		var matches []client.Merchant
		for _, it := range items {
			if strings.EqualFold(it.DisplayName, name.ValueString()) {
				matches = append(matches, it)
			}
		}
		switch len(matches) {
		case 0:
			resp.Diagnostics.AddAttributeError(path.Root("display_name"), "No such merchant", "Ledger has no merchant with that display name.")
			return
		case 1:
			found = matches[0]
		default:
			resp.Diagnostics.AddAttributeError(path.Root("display_name"), "Ambiguous merchant name",
				fmt.Sprintf("%d merchants share that display name; look one up by id.", len(matches)))
			return
		}
	}
	aliases, diags := merchantAliasesValue(found)
	resp.Diagnostics.Append(diags...)
	set := func(n string, v any) { resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root(n), v)...) }
	set("id", found.ID)
	// A configured value must come back unchanged, so a name typed in another case is kept as written.
	if name.IsNull() {
		set("display_name", found.DisplayName)
	} else {
		set("display_name", name)
	}
	set("archived", found.Archived)
	set("archived_at", strOrNull(found.ArchivedAt))
	set("merged_into", strOrNull(found.MergedInto))
	set("version", found.Version)
	set("created_at", found.CreatedAt)
	set("updated_at", found.UpdatedAt)
	set("aliases", aliases)
}

// ---- ledger_merchants ----

type merchantsDataSource struct{ client *client.Client }

// NewMerchantsDataSource is the data source constructor.
func NewMerchantsDataSource() datasource.DataSource { return &merchantsDataSource{} }

func (d *merchantsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_merchants"
}

func (d *merchantsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists Ledger merchants, in identifier order. " + merchantGrantNote,
		Attributes: map[string]schema.Attribute{
			"include_archived": schema.BoolAttribute{Optional: true,
				MarkdownDescription: "List archived and merged merchants too. Only active merchants are listed when unset or `false` (the API has no archived-only filter)."},
			"items": schema.ListNestedAttribute{Computed: true, MarkdownDescription: "The merchants.",
				NestedObject: schema.NestedAttributeObject{Attributes: merchantItemSchema()}},
		},
	}
}

func (d *merchantsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *merchantsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var include types.Bool
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("include_archived"), &include)...)
	if resp.Diagnostics.HasError() {
		return
	}
	items, err := d.client.ListMerchants(ctx, include.ValueBool())
	if err != nil {
		resp.Diagnostics.AddError("Listing merchants failed", err.Error())
		return
	}
	vals := make([]attr.Value, 0, len(items))
	for _, it := range items {
		v, diags := merchantValue(it)
		resp.Diagnostics.Append(diags...)
		vals = append(vals, v)
	}
	if resp.Diagnostics.HasError() {
		return
	}
	list, diags := types.ListValue(types.ObjectType{AttrTypes: merchantAttrTypes}, vals)
	resp.Diagnostics.Append(diags...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("items"), list)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("include_archived"), include)...)
}
