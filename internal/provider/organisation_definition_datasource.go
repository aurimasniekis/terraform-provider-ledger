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

// definitionDataSource reads one category, label or bucket by identifier or by name.
type definitionDataSource struct {
	kind     client.DefinitionKind
	typeName string
	client   *client.Client
}

// definitionsDataSource lists categories, labels or buckets.
type definitionsDataSource struct {
	kind     client.DefinitionKind
	typeName string // singular
	plural   string
	client   *client.Client
}

var (
	_ datasource.DataSource              = (*definitionDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*definitionDataSource)(nil)
	_ datasource.DataSource              = (*definitionsDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*definitionsDataSource)(nil)
)

// Data source constructors.
func NewCategoryDataSource() datasource.DataSource {
	return &definitionDataSource{kind: client.Categories, typeName: "category"}
}
func NewLabelDataSource() datasource.DataSource {
	return &definitionDataSource{kind: client.Labels, typeName: "label"}
}
func NewBucketDataSource() datasource.DataSource {
	return &definitionDataSource{kind: client.Buckets, typeName: "bucket"}
}
func NewCategoriesDataSource() datasource.DataSource {
	return &definitionsDataSource{kind: client.Categories, typeName: "category", plural: "categories"}
}
func NewLabelsDataSource() datasource.DataSource {
	return &definitionsDataSource{kind: client.Labels, typeName: "label", plural: "labels"}
}
func NewBucketsDataSource() datasource.DataSource {
	return &definitionsDataSource{kind: client.Buckets, typeName: "bucket", plural: "buckets"}
}

// itemAttrTypes is the shape of one definition in a list or a single read.
func itemAttrTypes(hasParent bool) map[string]attr.Type {
	m := map[string]attr.Type{
		"id": types.StringType, "name": types.StringType, "archived": types.BoolType, "archived_at": types.StringType,
		"version": types.Int64Type, "created_at": types.StringType, "updated_at": types.StringType,
	}
	if hasParent {
		m["parent_id"] = types.StringType
	}
	return m
}

func itemValue(d client.Definition, hasParent bool) (attr.Value, diag.Diagnostics) {
	m := map[string]attr.Value{
		"id": types.StringValue(d.ID), "name": types.StringValue(d.Name), "archived": types.BoolValue(d.Archived),
		"archived_at": strOrNull(d.ArchivedAt), "version": types.Int64Value(d.Version),
		"created_at": types.StringValue(d.CreatedAt), "updated_at": types.StringValue(d.UpdatedAt),
	}
	if hasParent {
		m["parent_id"] = strOrNull(d.ParentID)
	}
	return types.ObjectValue(itemAttrTypes(hasParent), m)
}

func (d *definitionDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_" + d.typeName
}

func (d *definitionDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := map[string]schema.Attribute{
		"id": schema.StringAttribute{
			Optional: true, Computed: true,
			MarkdownDescription: "The Ledger identifier. Set `id` or `name`, not both.",
			Validators: []validator.String{
				idValidator(),
				stringvalidator.ExactlyOneOf(path.MatchRoot("id"), path.MatchRoot("name")),
			},
		},
		"name": schema.StringAttribute{
			Optional: true, Computed: true,
			MarkdownDescription: "The name, matched ignoring case (Ledger names are unique ignoring case); it is returned as written. Archived ones are found too. Set `id` or `name`, not both.",
		},
		"archived":    schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether it is archived."},
		"archived_at": schema.StringAttribute{Computed: true, MarkdownDescription: "When it was archived (RFC 3339, UTC), or null."},
		"version":     schema.Int64Attribute{Computed: true, MarkdownDescription: "The optimistic-concurrency version."},
		"created_at":  schema.StringAttribute{Computed: true, MarkdownDescription: "When it was created (RFC 3339, UTC)."},
		"updated_at":  schema.StringAttribute{Computed: true, MarkdownDescription: "When it last changed (RFC 3339, UTC)."},
	}
	md := "Reads one Ledger " + d.typeName + " by identifier or by name."
	if d.kind == client.Categories {
		attrs["parent_id"] = schema.StringAttribute{
			Optional: true, Computed: true,
			MarkdownDescription: "The parent category's identifier, or null for a top-level category. When looking up by `name`, set it to narrow the " +
				"search to the children of that parent, or to `none` for top-level categories; a name shared by a top-level category and a child is otherwise ambiguous.",
		}
	}
	resp.Schema = schema.Schema{MarkdownDescription: md, Attributes: attrs}
}

func (d *definitionDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

// parentMatches reports whether a category's parent is the one parent_id names; "none" means a top-level category.
func parentMatches(actual *string, want string) bool {
	if want == "none" {
		return actual == nil
	}
	return actual != nil && *actual == want
}

func (d *definitionDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var id, name, parent types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("id"), &id)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("name"), &name)...)
	if d.kind == client.Categories {
		resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("parent_id"), &parent)...)
	}
	if resp.Diagnostics.HasError() {
		return
	}
	var found client.Definition
	if !id.IsNull() {
		def, err := d.client.GetDefinition(ctx, d.kind, id.ValueString())
		if err != nil {
			if client.IsNotFound(err) {
				resp.Diagnostics.AddAttributeError(path.Root("id"), "No such "+d.typeName, "Ledger has no "+d.typeName+" with that identifier.")
				return
			}
			resp.Diagnostics.AddError("Reading the "+d.typeName+" failed", err.Error())
			return
		}
		found = def
	} else {
		f := client.DefinitionFilter{NamePrefix: name.ValueString()}
		if p := nullableString(parent); p != nil {
			f.ParentID = p
		}
		items, err := d.client.ListDefinitions(ctx, d.kind, f)
		if err != nil {
			resp.Diagnostics.AddError("Looking up the "+d.typeName+" failed", err.Error())
			return
		}
		var matches []client.Definition
		for _, it := range items {
			if strings.EqualFold(it.Name, name.ValueString()) {
				matches = append(matches, it)
			}
		}
		switch len(matches) {
		case 0:
			resp.Diagnostics.AddAttributeError(path.Root("name"), "No such "+d.typeName, "Ledger has no "+d.typeName+" with that name.")
			return
		case 1:
			found = matches[0]
		default:
			resp.Diagnostics.AddAttributeError(path.Root("name"), "Ambiguous "+d.typeName+" name",
				fmt.Sprintf("%d categories share that name under different parents; set parent_id (or \"none\" for a top-level category) or look it up by id.", len(matches)))
			return
		}
	}
	if !id.IsNull() && !parent.IsNull() && !parentMatches(found.ParentID, parent.ValueString()) {
		resp.Diagnostics.AddAttributeError(path.Root("parent_id"), "Parent does not match", "The category with that id has a different parent than parent_id says.")
		return
	}
	set := func(n string, v any) { resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root(n), v)...) }
	set("id", found.ID)
	// A value given in the configuration must come back unchanged (Terraform rejects a data source that rewrites it), so
	// a name typed in another case, or a parent_id of "none", is kept as written.
	if name.IsNull() {
		set("name", found.Name)
	} else {
		set("name", name)
	}
	set("archived", found.Archived)
	set("archived_at", strOrNull(found.ArchivedAt))
	set("version", found.Version)
	set("created_at", found.CreatedAt)
	set("updated_at", found.UpdatedAt)
	if d.kind == client.Categories {
		if parent.IsNull() {
			set("parent_id", strOrNull(found.ParentID))
		} else {
			set("parent_id", parent)
		}
	}
}

func (d *definitionsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_" + d.plural
}

func (d *definitionsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	item := map[string]schema.Attribute{
		"id":          schema.StringAttribute{Computed: true, MarkdownDescription: "The Ledger identifier."},
		"name":        schema.StringAttribute{Computed: true, MarkdownDescription: "The name."},
		"archived":    schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether it is archived."},
		"archived_at": schema.StringAttribute{Computed: true, MarkdownDescription: "When it was archived (RFC 3339, UTC), or null."},
		"version":     schema.Int64Attribute{Computed: true, MarkdownDescription: "The optimistic-concurrency version."},
		"created_at":  schema.StringAttribute{Computed: true, MarkdownDescription: "When it was created (RFC 3339, UTC)."},
		"updated_at":  schema.StringAttribute{Computed: true, MarkdownDescription: "When it last changed (RFC 3339, UTC)."},
	}
	attrs := map[string]schema.Attribute{
		"archived": schema.BoolAttribute{Optional: true, MarkdownDescription: "Only archived (`true`) or only active (`false`) ones. Both are listed when unset."},
		"name_prefix": schema.StringAttribute{
			Optional: true, MarkdownDescription: "Only names starting with this text, ignoring case.",
		},
	}
	if d.kind == client.Categories {
		item["parent_id"] = schema.StringAttribute{Computed: true, MarkdownDescription: "The parent category's identifier, or null for a top-level category."}
		attrs["parent_id"] = schema.StringAttribute{
			Optional:            true,
			MarkdownDescription: "Only the children of this category, or `none` for the top-level categories.",
		}
	}
	attrs["items"] = schema.ListNestedAttribute{
		Computed:            true,
		MarkdownDescription: "The matching " + d.plural + ", in creation order.",
		NestedObject:        schema.NestedAttributeObject{Attributes: item},
	}
	resp.Schema = schema.Schema{MarkdownDescription: "Lists Ledger " + d.plural + ", optionally filtered.", Attributes: attrs}
}

func (d *definitionsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *definitionsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var archived types.Bool
	var prefix, parent types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("archived"), &archived)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("name_prefix"), &prefix)...)
	if d.kind == client.Categories {
		resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("parent_id"), &parent)...)
	}
	if resp.Diagnostics.HasError() {
		return
	}
	f := client.DefinitionFilter{NamePrefix: prefix.ValueString(), ParentID: nullableString(parent)}
	if !archived.IsNull() && !archived.IsUnknown() {
		b := archived.ValueBool()
		f.Archived = &b
	}
	items, err := d.client.ListDefinitions(ctx, d.kind, f)
	if err != nil {
		resp.Diagnostics.AddError("Listing "+d.plural+" failed", err.Error())
		return
	}
	vals := make([]attr.Value, 0, len(items))
	for _, it := range items {
		v, diags := itemValue(it, d.kind == client.Categories)
		resp.Diagnostics.Append(diags...)
		vals = append(vals, v)
	}
	if resp.Diagnostics.HasError() {
		return
	}
	list, diags := types.ListValue(types.ObjectType{AttrTypes: itemAttrTypes(d.kind == client.Categories)}, vals)
	resp.Diagnostics.Append(diags...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("items"), list)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("archived"), archived)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name_prefix"), prefix)...)
	if d.kind == client.Categories {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("parent_id"), parent)...)
	}
}
