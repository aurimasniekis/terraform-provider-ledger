package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aurimasniekis/terraform-provider-ledger/internal/client"
)

var (
	_ datasource.DataSource              = (*apiTokenDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*apiTokenDataSource)(nil)
	_ datasource.DataSource              = (*apiTokensDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*apiTokensDataSource)(nil)
)

const apiTokenMetadataNote = "**Metadata only**: Ledger never serves a token's secret again, and these data sources have no secret attribute. " + apiTokenGrantNote

func apiTokenItemSchema() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"id":              schema.StringAttribute{Computed: true, MarkdownDescription: "The Ledger identifier."},
		"name":            schema.StringAttribute{Computed: true, MarkdownDescription: "The token's name."},
		"owner_id":        schema.StringAttribute{Computed: true, MarkdownDescription: "The user that owns the token."},
		"root":            schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether it is a root token."},
		"grants":          schema.SetAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "The token's own grants (empty for a root token). Requests get only those the owner's current role allows."},
		"hint":            schema.StringAttribute{Computed: true, MarkdownDescription: "The prefix and the first and last characters, to recognise the token."},
		"prefix":          schema.StringAttribute{Computed: true, MarkdownDescription: "`ldgr_` and the first four random characters."},
		"last_chars":      schema.StringAttribute{Computed: true, MarkdownDescription: "The last four characters."},
		"status":          schema.StringAttribute{Computed: true, MarkdownDescription: "`active`, `expired` or `revoked`."},
		"created_at":      schema.StringAttribute{Computed: true, MarkdownDescription: "When it was created (RFC 3339, UTC)."},
		"expires_at":      schema.StringAttribute{Computed: true, MarkdownDescription: "When it expires, or null when it never does."},
		"last_used_at":    schema.StringAttribute{Computed: true, MarkdownDescription: "When it was last used (at most once a minute), or null."},
		"revoked_at":      schema.StringAttribute{Computed: true, MarkdownDescription: "When it was revoked, or null."},
		"parent_token_id": schema.StringAttribute{Computed: true, MarkdownDescription: "The token that created it through the API, or null."},
	}
}

// ---- ledger_api_token ----

type apiTokenDataSource struct{ client *client.Client }

// NewAPITokenDataSource is the data source constructor.
func NewAPITokenDataSource() datasource.DataSource { return &apiTokenDataSource{} }

func (d *apiTokenDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_api_token"
}

func (d *apiTokenDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := apiTokenItemSchema()
	attrs["id"] = schema.StringAttribute{Required: true, Validators: []validator.String{idValidator()}, MarkdownDescription: "The Ledger identifier of the token."}
	resp.Schema = schema.Schema{MarkdownDescription: "Reads the metadata of one API token by identifier. " + apiTokenMetadataNote, Attributes: attrs}
}

func (d *apiTokenDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *apiTokenDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var id types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("id"), &id)...)
	if resp.Diagnostics.HasError() {
		return
	}
	t, err := d.client.GetAPIToken(ctx, id.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.Diagnostics.AddAttributeError(path.Root("id"), "No such API token", "Ledger has no API token with that identifier.")
			return
		}
		resp.Diagnostics.AddError("Reading the API token failed", err.Error())
		return
	}
	v, diags := apiTokenValue(t)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	obj := v.(types.Object)
	for k, a := range obj.Attributes() {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root(k), a)...)
	}
}

// ---- ledger_api_tokens ----

type apiTokensDataSource struct{ client *client.Client }

// NewAPITokensDataSource is the data source constructor.
func NewAPITokensDataSource() datasource.DataSource { return &apiTokensDataSource{} }

func (d *apiTokensDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_api_tokens"
}

func (d *apiTokensDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the API tokens of every user, in identifier order, expired and revoked ones included unless filtered. " + apiTokenMetadataNote,
		Attributes: map[string]schema.Attribute{
			"owner_id": schema.StringAttribute{Optional: true, Validators: []validator.String{idValidator()}, MarkdownDescription: "Only this user's tokens."},
			"statuses": schema.ListAttribute{Optional: true, ElementType: types.StringType,
				Validators:          []validator.List{listvalidator.ValueStringsAre(stringvalidator.OneOf(client.TokenActive, client.TokenExpired, client.TokenRevoked))},
				MarkdownDescription: "Only tokens with one of these statuses (`active`, `expired`, `revoked`); filtered here, not by Ledger."},
			"items": schema.ListNestedAttribute{Computed: true, MarkdownDescription: "The tokens.",
				NestedObject: schema.NestedAttributeObject{Attributes: apiTokenItemSchema()}},
		},
	}
}

func (d *apiTokensDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *apiTokensDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var owner types.String
	var statuses types.List
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("owner_id"), &owner)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("statuses"), &statuses)...)
	if resp.Diagnostics.HasError() {
		return
	}
	want := map[string]bool{}
	if !statuses.IsNull() {
		var s []string
		resp.Diagnostics.Append(statuses.ElementsAs(ctx, &s, false)...)
		for _, x := range s {
			want[x] = true
		}
	}
	items, err := d.client.ListAPITokens(ctx, client.APITokenFilter{OwnerID: owner.ValueString()})
	if err != nil {
		resp.Diagnostics.AddError("Listing API tokens failed", err.Error())
		return
	}
	vals := make([]attr.Value, 0, len(items))
	for _, it := range items {
		if len(want) > 0 && !want[it.Status] {
			continue
		}
		v, diags := apiTokenValue(it)
		resp.Diagnostics.Append(diags...)
		vals = append(vals, v)
	}
	if resp.Diagnostics.HasError() {
		return
	}
	list, diags := types.ListValue(types.ObjectType{AttrTypes: apiTokenAttrTypes}, vals)
	resp.Diagnostics.Append(diags...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("items"), list)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("owner_id"), owner)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("statuses"), statuses)...)
}
