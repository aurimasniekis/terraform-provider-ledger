package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aurimasniekis/terraform-provider-ledger/internal/client"
)

// The assignment resources record the person's own decisions (origin user). Ledger's rules and models can assign too;
// they never replace the person's word, so a decision made here survives refreshes and automatic runs.

var replaceOnChange = []planmodifier.String{stringplanmodifier.RequiresReplace()}

var (
	_ resource.Resource                = (*categoryAssignmentResource)(nil)
	_ resource.ResourceWithConfigure   = (*categoryAssignmentResource)(nil)
	_ resource.ResourceWithImportState = (*categoryAssignmentResource)(nil)
	_ resource.Resource                = (*labelAssignmentResource)(nil)
	_ resource.ResourceWithConfigure   = (*labelAssignmentResource)(nil)
	_ resource.ResourceWithImportState = (*labelAssignmentResource)(nil)
	_ resource.Resource                = (*bucketMemberResource)(nil)
	_ resource.ResourceWithConfigure   = (*bucketMemberResource)(nil)
	_ resource.ResourceWithImportState = (*bucketMemberResource)(nil)
)

var assignableObjectTypes = []client.ObjectType{client.ObjectTransaction, client.ObjectCommitment, client.ObjectAccount, client.ObjectMerchant}

const objectTypeDoc = "The kind of object: `transaction`, `commitment`, `account` or `merchant`. A merchant is addressed by its record identifier (`ledger_merchant`) and needs an API token with the administrator-only `merchants:read` and `merchants:write` grants, because merchant names derive from bank text; an archived merchant takes no new assignment. Buckets never hold merchants. Changing it replaces the resource."

// ---- ledger_category_assignment ----

type categoryAssignmentResource struct{ client *client.Client }

// NewCategoryAssignmentResource is the resource constructor.
func NewCategoryAssignmentResource() resource.Resource { return &categoryAssignmentResource{} }

type categoryAssignmentModel struct {
	ID         types.String `tfsdk:"id"`
	ObjectType types.String `tfsdk:"object_type"`
	ObjectID   types.String `tfsdk:"object_id"`
	CategoryID types.String `tfsdk:"category_id"`
}

func (r *categoryAssignmentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_category_assignment"
}

func (r *categoryAssignmentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The person's category decision for one object. A category is single-valued, so an object has at most one of these. " +
			"Rules and models never replace it. **Destroying** it hands the decision back (the object's own category row is removed), so a rule or model may assign again.\n\n" +
			"Import with `<object_type>/<object_id>`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, MarkdownDescription: "`<object_type>/<object_id>`.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"object_type": schema.StringAttribute{Required: true, MarkdownDescription: objectTypeDoc, PlanModifiers: replaceOnChange,
				Validators: []validator.String{objectTypeValidator(assignableObjectTypes...)}},
			"object_id": schema.StringAttribute{Required: true, MarkdownDescription: "The Ledger identifier of the object. Changing it replaces the resource.", PlanModifiers: replaceOnChange,
				Validators: []validator.String{idValidator()}},
			"category_id": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "The identifier of an active category. **Omit it to record that the person chose no category**: Ledger remembers " +
					"that decision, and rules and models respect it. An archived category is refused.",
				Validators: []validator.String{idValidator()},
			},
		},
	}
}

func (r *categoryAssignmentResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (r *categoryAssignmentResource) apply(ctx context.Context, m *categoryAssignmentModel, diagsFn func(string, string)) {
	_, err := r.client.SetObjectCategory(ctx, client.ObjectType(m.ObjectType.ValueString()), m.ObjectID.ValueString(), nullableString(m.CategoryID))
	if err != nil {
		diagsFn("Setting the category failed", err.Error())
		return
	}
	m.ID = types.StringValue(m.ObjectType.ValueString() + "/" + m.ObjectID.ValueString())
}

func (r *categoryAssignmentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m categoryAssignmentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.apply(ctx, &m, func(s, d string) { resp.Diagnostics.AddError(s, d) })
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *categoryAssignmentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m categoryAssignmentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.apply(ctx, &m, func(s, d string) { resp.Diagnostics.AddError(s, d) })
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *categoryAssignmentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m categoryAssignmentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	a, err := r.client.GetObjectOrganisation(ctx, client.ObjectType(m.ObjectType.ValueString()), m.ObjectID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Reading the category assignment failed", err.Error())
		return
	}
	// Only the person's own, direct decision belongs to this resource: an automatic assignment or one inherited from a
	// pending transaction is not it, and a person's decision that was reset out of band is gone.
	if a.Category == nil || a.Category.Origin != "user" || a.Category.Inherited {
		resp.State.RemoveResource(ctx)
		return
	}
	if a.Category.Category == nil {
		m.CategoryID = types.StringNull()
	} else {
		m.CategoryID = types.StringValue(a.Category.Category.ID)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *categoryAssignmentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m categoryAssignmentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.ResetObjectCategory(ctx, client.ObjectType(m.ObjectType.ValueString()), m.ObjectID.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Resetting the category failed", err.Error())
	}
}

func (r *categoryAssignmentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts, err := splitImportID(req.ID, 2, "<object_type>/<object_id>")
	if err != nil {
		resp.Diagnostics.AddError("Invalid import identifier", err.Error())
		return
	}
	if !validAssignable(parts[0]) || !client.ValidID(parts[1]) {
		resp.Diagnostics.AddError("Invalid import identifier", "The object type must be transaction, commitment, account or merchant, and the object id a Ledger identifier.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("object_type"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("object_id"), parts[1])...)
}

func validAssignable(t string) bool {
	for _, a := range assignableObjectTypes {
		if string(a) == t {
			return true
		}
	}
	return false
}

// ---- ledger_label_assignment ----

type labelAssignmentResource struct{ client *client.Client }

// NewLabelAssignmentResource is the resource constructor.
func NewLabelAssignmentResource() resource.Resource { return &labelAssignmentResource{} }

type labelAssignmentModel struct {
	ID         types.String `tfsdk:"id"`
	ObjectType types.String `tfsdk:"object_type"`
	ObjectID   types.String `tfsdk:"object_id"`
	LabelID    types.String `tfsdk:"label_id"`
}

func (r *labelAssignmentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_label_assignment"
}

func (r *labelAssignmentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "One label put on one object by the person. Labels are many-valued: an object can carry several, each its own resource. " +
			"**Destroying** it takes the label off and Ledger records that the person removed it, so rules and models do not add it back; put it back by applying the resource again.\n\n" +
			"Import with `<object_type>/<object_id>/<label_id>`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, MarkdownDescription: "`<object_type>/<object_id>/<label_id>`.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"object_type": schema.StringAttribute{Required: true, MarkdownDescription: objectTypeDoc, PlanModifiers: replaceOnChange,
				Validators: []validator.String{objectTypeValidator(assignableObjectTypes...)}},
			"object_id": schema.StringAttribute{Required: true, MarkdownDescription: "The Ledger identifier of the object. Changing it replaces the resource.", PlanModifiers: replaceOnChange,
				Validators: []validator.String{idValidator()}},
			"label_id": schema.StringAttribute{Required: true, MarkdownDescription: "The identifier of an active label. Changing it replaces the resource.", PlanModifiers: replaceOnChange,
				Validators: []validator.String{idValidator()}},
		},
	}
}

func (r *labelAssignmentResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (r *labelAssignmentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m labelAssignmentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if _, err := r.client.AddObjectLabel(ctx, client.ObjectType(m.ObjectType.ValueString()), m.ObjectID.ValueString(), m.LabelID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Putting the label on the object failed", err.Error())
		return
	}
	m.ID = types.StringValue(m.ObjectType.ValueString() + "/" + m.ObjectID.ValueString() + "/" + m.LabelID.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// Update is never called: every attribute forces replacement.
func (r *labelAssignmentResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Unexpected update", "A label assignment has no updatable attribute. This is a provider bug.")
}

func (r *labelAssignmentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m labelAssignmentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	a, err := r.client.GetObjectOrganisation(ctx, client.ObjectType(m.ObjectType.ValueString()), m.ObjectID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Reading the label assignment failed", err.Error())
		return
	}
	for _, l := range a.Labels {
		if l.Label.ID == m.LabelID.ValueString() && l.Origin == "user" && !l.Inherited {
			resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
			return
		}
	}
	resp.State.RemoveResource(ctx)
}

func (r *labelAssignmentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m labelAssignmentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.RemoveObjectLabel(ctx, client.ObjectType(m.ObjectType.ValueString()), m.ObjectID.ValueString(), m.LabelID.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Taking the label off the object failed", err.Error())
	}
}

func (r *labelAssignmentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts, err := splitImportID(req.ID, 3, "<object_type>/<object_id>/<label_id>")
	if err != nil {
		resp.Diagnostics.AddError("Invalid import identifier", err.Error())
		return
	}
	if !validAssignable(parts[0]) || !client.ValidID(parts[1]) || !client.ValidID(parts[2]) {
		resp.Diagnostics.AddError("Invalid import identifier", "The object type must be transaction, commitment, account or merchant, and both identifiers Ledger identifiers.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("object_type"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("object_id"), parts[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("label_id"), parts[2])...)
}

// ---- ledger_bucket_member ----

type bucketMemberResource struct{ client *client.Client }

// NewBucketMemberResource is the resource constructor.
func NewBucketMemberResource() resource.Resource { return &bucketMemberResource{} }

type bucketMemberModel struct {
	ID         types.String `tfsdk:"id"`
	BucketID   types.String `tfsdk:"bucket_id"`
	ObjectType types.String `tfsdk:"object_type"`
	ObjectID   types.String `tfsdk:"object_id"`
	AddedAt    types.String `tfsdk:"added_at"`
}

func (r *bucketMemberResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket_member"
}

func (r *bucketMemberResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A commitment or account in a bucket. Buckets group commitments and accounts only, and hold no money. " +
			"The bucket must be active to add a member; removing a member is allowed from an archived bucket too.\n\n" +
			"Import with `<bucket_id>/<object_type>/<object_id>`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, MarkdownDescription: "`<bucket_id>/<object_type>/<object_id>`.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"bucket_id": schema.StringAttribute{Required: true, MarkdownDescription: "The identifier of the bucket. Changing it replaces the resource.", PlanModifiers: replaceOnChange,
				Validators: []validator.String{idValidator()}},
			"object_type": schema.StringAttribute{Required: true, MarkdownDescription: "The kind of object: `commitment` or `account`. Changing it replaces the resource.", PlanModifiers: replaceOnChange,
				Validators: []validator.String{objectTypeValidator(client.ObjectCommitment, client.ObjectAccount)}},
			"object_id": schema.StringAttribute{Required: true, MarkdownDescription: "The Ledger identifier of the commitment or account. Changing it replaces the resource.", PlanModifiers: replaceOnChange,
				Validators: []validator.String{idValidator()}},
			"added_at": schema.StringAttribute{Computed: true, MarkdownDescription: "When it was added (RFC 3339, UTC).", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		},
	}
}

func (r *bucketMemberResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

// find reads the member back; it returns nil when the bucket or the member is gone.
func (r *bucketMemberResource) find(ctx context.Context, m bucketMemberModel) (*client.BucketMember, error) {
	members, err := r.client.ListBucketMembers(ctx, m.BucketID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	for i := range members {
		if members[i].ObjectType == m.ObjectType.ValueString() && members[i].ObjectID == m.ObjectID.ValueString() {
			return &members[i], nil
		}
	}
	return nil, nil
}

func (r *bucketMemberResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m bucketMemberModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.AddBucketMember(ctx, m.BucketID.ValueString(), client.ObjectType(m.ObjectType.ValueString()), m.ObjectID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Adding the bucket member failed", err.Error())
		return
	}
	mem, err := r.find(ctx, m)
	if err != nil || mem == nil {
		detail := "The member was added but could not be read back."
		if err != nil {
			detail += " " + err.Error()
		}
		resp.Diagnostics.AddError("Reading the bucket member failed", detail)
		return
	}
	m.ID = types.StringValue(fmt.Sprintf("%s/%s/%s", m.BucketID.ValueString(), m.ObjectType.ValueString(), m.ObjectID.ValueString()))
	m.AddedAt = types.StringValue(mem.AddedAt)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// Update is never called: every attribute forces replacement.
func (r *bucketMemberResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Unexpected update", "A bucket member has no updatable attribute. This is a provider bug.")
}

func (r *bucketMemberResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m bucketMemberModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	mem, err := r.find(ctx, m)
	if err != nil {
		resp.Diagnostics.AddError("Reading the bucket member failed", err.Error())
		return
	}
	if mem == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	m.AddedAt = types.StringValue(mem.AddedAt)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *bucketMemberResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m bucketMemberModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.RemoveBucketMember(ctx, m.BucketID.ValueString(), client.ObjectType(m.ObjectType.ValueString()), m.ObjectID.ValueString())
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Removing the bucket member failed", err.Error())
	}
}

func (r *bucketMemberResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts, err := splitImportID(req.ID, 3, "<bucket_id>/<object_type>/<object_id>")
	if err != nil {
		resp.Diagnostics.AddError("Invalid import identifier", err.Error())
		return
	}
	if !client.ValidID(parts[0]) || (parts[1] != "commitment" && parts[1] != "account") || !client.ValidID(parts[2]) {
		resp.Diagnostics.AddError("Invalid import identifier", "The bucket and object must be Ledger identifiers and the object type commitment or account.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("bucket_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("object_type"), parts[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("object_id"), parts[2])...)
}
