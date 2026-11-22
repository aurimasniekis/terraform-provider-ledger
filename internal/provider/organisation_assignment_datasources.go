package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aurimasniekis/terraform-provider-ledger/internal/client"
)

var (
	_ datasource.DataSource              = (*objectOrganisationDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*objectOrganisationDataSource)(nil)
	_ datasource.DataSource              = (*bucketMembersDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*bucketMembersDataSource)(nil)
)

// ---- ledger_object_organisation ----

type objectOrganisationDataSource struct{ client *client.Client }

// NewObjectOrganisationDataSource is the data source constructor.
func NewObjectOrganisationDataSource() datasource.DataSource { return &objectOrganisationDataSource{} }

type objectOrganisationModel struct {
	ObjectType      types.String `tfsdk:"object_type"`
	ObjectID        types.String `tfsdk:"object_id"`
	CategoryID      types.String `tfsdk:"category_id"`
	CategoryOrigin  types.String `tfsdk:"category_origin"`
	CategoryNone    types.Bool   `tfsdk:"category_none"`
	LabelIDs        types.Set    `tfsdk:"label_ids"`
	RemovedLabelIDs types.Set    `tfsdk:"removed_label_ids"`
	BucketIDs       types.Set    `tfsdk:"bucket_ids"`
}

func (d *objectOrganisationDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_object_organisation"
}

func (d *objectOrganisationDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "What is assigned to one object: its category, its labels, the labels the person removed and its buckets. " +
			"Read-only, whatever the origin of each assignment (the person, a rule or a model). Use `ledger_category_assignment`, " +
			"`ledger_label_assignment` and `ledger_bucket_member` to manage the person's own assignments.",
		Attributes: map[string]schema.Attribute{
			"object_type": schema.StringAttribute{Required: true, MarkdownDescription: objectTypeDoc,
				Validators: []validator.String{objectTypeValidator(assignableObjectTypes...)}},
			"object_id":         schema.StringAttribute{Required: true, MarkdownDescription: "The Ledger identifier of the object.", Validators: []validator.String{idValidator()}},
			"category_id":       schema.StringAttribute{Computed: true, MarkdownDescription: "The category's identifier; null when the object has no category, or when the person chose none (see `category_none`)."},
			"category_origin":   schema.StringAttribute{Computed: true, MarkdownDescription: "Who set the category: `user`, `rule` or `model`; null when no category decision exists."},
			"category_none":     schema.BoolAttribute{Computed: true, MarkdownDescription: "True when the person explicitly chose no category."},
			"label_ids":         schema.SetAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Identifiers of the labels on the object."},
			"removed_label_ids": schema.SetAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Identifiers of the labels the person took off; rules and models do not add them back."},
			"bucket_ids":        schema.SetAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Identifiers of the buckets the object is in (commitments and accounts only)."},
		},
	}
}

func (d *objectOrganisationDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func stringSet(ids []string) types.Set {
	vals := make([]attr.Value, len(ids))
	for i, id := range ids {
		vals[i] = types.StringValue(id)
	}
	s, _ := types.SetValue(types.StringType, vals)
	return s
}

func (d *objectOrganisationDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m objectOrganisationModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	a, err := d.client.GetObjectOrganisation(ctx, client.ObjectType(m.ObjectType.ValueString()), m.ObjectID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.Diagnostics.AddAttributeError(path.Root("object_id"), "No such object", "Ledger has no "+m.ObjectType.ValueString()+" with that identifier.")
			return
		}
		resp.Diagnostics.AddError("Reading the object's organisation failed", err.Error())
		return
	}
	m.CategoryID, m.CategoryOrigin, m.CategoryNone = types.StringNull(), types.StringNull(), types.BoolValue(false)
	if a.Category != nil {
		m.CategoryOrigin = types.StringValue(a.Category.Origin)
		if a.Category.Category == nil {
			m.CategoryNone = types.BoolValue(true)
		} else {
			m.CategoryID = types.StringValue(a.Category.Category.ID)
		}
	}
	var labels, removed, buckets []string
	for _, l := range a.Labels {
		labels = append(labels, l.Label.ID)
	}
	for _, l := range a.RemovedLabels {
		removed = append(removed, l.Label.ID)
	}
	for _, b := range a.Buckets {
		buckets = append(buckets, b.ID)
	}
	m.LabelIDs, m.RemovedLabelIDs, m.BucketIDs = stringSet(labels), stringSet(removed), stringSet(buckets)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// ---- ledger_bucket_members ----

type bucketMembersDataSource struct{ client *client.Client }

// NewBucketMembersDataSource is the data source constructor.
func NewBucketMembersDataSource() datasource.DataSource { return &bucketMembersDataSource{} }

var memberAttrTypes = map[string]attr.Type{"object_type": types.StringType, "object_id": types.StringType, "added_at": types.StringType}

func (d *bucketMembersDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket_members"
}

func (d *bucketMembersDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The commitments and accounts in one bucket, ordered by object type then identifier. An archived bucket keeps its members.",
		Attributes: map[string]schema.Attribute{
			"bucket_id": schema.StringAttribute{Required: true, MarkdownDescription: "The identifier of the bucket.", Validators: []validator.String{idValidator()}},
			"members": schema.ListNestedAttribute{
				Computed: true, MarkdownDescription: "The members.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"object_type": schema.StringAttribute{Computed: true, MarkdownDescription: "`commitment` or `account`."},
					"object_id":   schema.StringAttribute{Computed: true, MarkdownDescription: "The member's identifier."},
					"added_at":    schema.StringAttribute{Computed: true, MarkdownDescription: "When it was added (RFC 3339, UTC)."},
				}},
			},
		},
	}
}

func (d *bucketMembersDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *bucketMembersDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var bucket types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("bucket_id"), &bucket)...)
	if resp.Diagnostics.HasError() {
		return
	}
	members, err := d.client.ListBucketMembers(ctx, bucket.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.Diagnostics.AddAttributeError(path.Root("bucket_id"), "No such bucket", "Ledger has no bucket with that identifier.")
			return
		}
		resp.Diagnostics.AddError("Listing the bucket members failed", err.Error())
		return
	}
	vals := make([]attr.Value, 0, len(members))
	for _, mem := range members {
		v, diags := types.ObjectValue(memberAttrTypes, map[string]attr.Value{
			"object_type": types.StringValue(mem.ObjectType), "object_id": types.StringValue(mem.ObjectID), "added_at": types.StringValue(mem.AddedAt),
		})
		resp.Diagnostics.Append(diags...)
		vals = append(vals, v)
	}
	list, diags := types.ListValue(types.ObjectType{AttrTypes: memberAttrTypes}, vals)
	resp.Diagnostics.Append(diags...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("bucket_id"), bucket)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("members"), list)...)
}
