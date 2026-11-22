package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
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

// definitionResource manages one category, label or bucket. The three share a lifecycle (ADR-0053 of go-ledger: archive
// first, version-checked changes, delete only when unused or with reassignment), so one implementation serves all.
type definitionResource struct {
	kind     client.DefinitionKind
	typeName string // "category", "label" or "bucket"
	article  string
	client   *client.Client
}

var (
	_ resource.Resource                = (*definitionResource)(nil)
	_ resource.ResourceWithConfigure   = (*definitionResource)(nil)
	_ resource.ResourceWithImportState = (*definitionResource)(nil)
)

// NewCategoryResource, NewLabelResource and NewBucketResource are the resource constructors.
func NewCategoryResource() resource.Resource {
	return &definitionResource{kind: client.Categories, typeName: "category", article: "a"}
}
func NewLabelResource() resource.Resource {
	return &definitionResource{kind: client.Labels, typeName: "label", article: "a"}
}
func NewBucketResource() resource.Resource {
	return &definitionResource{kind: client.Buckets, typeName: "bucket", article: "a"}
}

func (r *definitionResource) hasParent() bool { return r.kind == client.Categories }

func (r *definitionResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_" + r.typeName
}

func (r *definitionResource) description() string {
	switch r.kind {
	case client.Categories:
		return "A Ledger category. A category is single-valued per object and has two levels: a top-level category or a child of one " +
			"(a child cannot be a parent). Names are unique ignoring case among siblings, archived categories included."
	case client.Labels:
		return "A Ledger label. Labels are many-valued and flat: an object can carry several. Names are unique ignoring case, " +
			"archived labels included."
	default:
		return "A Ledger bucket: a user-defined grouping of commitments and accounts used to slice the monthly outlook. " +
			"A bucket never holds, allocates or reserves money and is not a budget envelope. Names are unique ignoring case, archived buckets included."
	}
}

func (r *definitionResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	attrs := map[string]schema.Attribute{
		"id": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "The Ledger identifier (a ULID). It never changes, so a rename reaches every assignment.",
			PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		},
		"name": schema.StringAttribute{
			Required: true,
			MarkdownDescription: "The name, 1 to 80 characters. Ledger trims names and collapses white space, so write it that way. " +
				"Renaming keeps the identifier and therefore every assignment.",
			Validators: []validator.String{nameValidator()},
		},
		"archived": schema.BoolAttribute{
			Optional: true,
			Computed: true,
			Default:  booldefault.StaticBool(false),
			MarkdownDescription: "Whether the " + r.typeName + " is archived. Archiving keeps the " + r.typeName + " and every assignment to it " +
				"(history stays readable) and stops anything new from using it. Archive instead of destroying when anything is assigned. Defaults to `false`.",
		},
		"reassign_to": schema.StringAttribute{
			Optional: true,
			MarkdownDescription: "Used only when this resource is destroyed: the identifier of an active " + r.typeName + " that existing assignments " +
				"move to. Without it, destroying a " + r.typeName + " that is still assigned fails with `definition_in_use`. " +
				"The assignments become the person's own (origin user). Not read back from Ledger, so it is empty after an import.",
			Validators: []validator.String{idValidator()},
		},
		"version": schema.Int64Attribute{
			Computed:            true,
			MarkdownDescription: "The optimistic-concurrency version (the ETag). Changes are sent against the version Terraform last read; a stale one fails instead of overwriting.",
		},
		"archived_at": schema.StringAttribute{Computed: true, MarkdownDescription: "When it was archived (RFC 3339, UTC), or null."},
		"created_at":  schema.StringAttribute{Computed: true, MarkdownDescription: "When it was created (RFC 3339, UTC).", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		"updated_at":  schema.StringAttribute{Computed: true, MarkdownDescription: "When it last changed (RFC 3339, UTC)."},
	}
	if r.hasParent() {
		attrs["parent_id"] = schema.StringAttribute{
			Optional: true,
			MarkdownDescription: "The identifier of the parent category. The parent must be an active top-level category. Omit for a top-level category. " +
				"A category that has children cannot be given a parent.",
			Validators: []validator.String{idValidator()},
		}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: r.description() + "\n\nImport with the identifier: `terraform import ledger_" + r.typeName + ".example <id>`.",
		Attributes:          attrs,
	}
}

func (r *definitionResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

// defValues holds the writable attributes read from a plan or state.
type defValues struct {
	ID         types.String
	Name       types.String
	ParentID   types.String
	Archived   types.Bool
	ReassignTo types.String
	Version    types.Int64
}

func (r *definitionResource) read(ctx context.Context, g attrGetter) (defValues, diag.Diagnostics) {
	var v defValues
	var diags diag.Diagnostics
	diags.Append(g.GetAttribute(ctx, path.Root("id"), &v.ID)...)
	diags.Append(g.GetAttribute(ctx, path.Root("name"), &v.Name)...)
	diags.Append(g.GetAttribute(ctx, path.Root("archived"), &v.Archived)...)
	diags.Append(g.GetAttribute(ctx, path.Root("reassign_to"), &v.ReassignTo)...)
	diags.Append(g.GetAttribute(ctx, path.Root("version"), &v.Version)...)
	if r.hasParent() {
		diags.Append(g.GetAttribute(ctx, path.Root("parent_id"), &v.ParentID)...)
	}
	return v, diags
}

// write sets every attribute from the server's view, keeping reassign_to from keep (it is never read back).
func (r *definitionResource) write(ctx context.Context, st interface {
	SetAttribute(ctx context.Context, p path.Path, val any) diag.Diagnostics
}, d client.Definition, reassign types.String) diag.Diagnostics {
	var diags diag.Diagnostics
	set := func(name string, val any) { diags.Append(st.SetAttribute(ctx, path.Root(name), val)...) }
	set("id", d.ID)
	set("name", d.Name)
	set("archived", d.Archived)
	set("reassign_to", reassign)
	set("version", d.Version)
	set("archived_at", strOrNull(d.ArchivedAt))
	set("created_at", d.CreatedAt)
	set("updated_at", d.UpdatedAt)
	if r.hasParent() {
		set("parent_id", strOrNull(d.ParentID))
	}
	return diags
}

func (r *definitionResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	plan, diags := r.read(ctx, req.Plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	d, err := r.client.CreateDefinition(ctx, r.kind, plan.Name.ValueString(), nullableString(plan.ParentID))
	if err != nil {
		resp.Diagnostics.AddError("Creating the "+r.typeName+" failed", err.Error())
		return
	}
	// Record the created object first so a failed archive step leaves it tracked (Terraform then marks it tainted).
	resp.Diagnostics.Append(r.write(ctx, &resp.State, d, plan.ReassignTo)...)
	if plan.Archived.ValueBool() {
		a, err := r.client.ArchiveDefinition(ctx, r.kind, d.ID, d.Version)
		if err != nil {
			resp.Diagnostics.AddError("Archiving the new "+r.typeName+" failed", "It was created but is not archived. "+err.Error())
			return
		}
		resp.Diagnostics.Append(r.write(ctx, &resp.State, a, plan.ReassignTo)...)
	}
}

func (r *definitionResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	st, diags := r.read(ctx, req.State)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	d, err := r.client.GetDefinition(ctx, r.kind, st.ID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Reading the "+r.typeName+" failed", err.Error())
		return
	}
	resp.Diagnostics.Append(r.write(ctx, &resp.State, d, st.ReassignTo)...)
}

func (r *definitionResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	plan, diags := r.read(ctx, req.Plan)
	resp.Diagnostics.Append(diags...)
	st, diags := r.read(ctx, req.State)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := st.ID.ValueString()
	version := st.Version.ValueInt64()
	var cur *client.Definition
	fail := func(what string, err error) {
		detail := err.Error()
		if client.IsVersionConflict(err) {
			detail = versionConflictDetail(err)
		}
		resp.Diagnostics.AddError(what, detail)
	}
	step := func(d client.Definition, err error, what string) bool {
		if err != nil {
			fail(what, err)
			return false
		}
		cur, version = &d, d.Version
		return true
	}

	wantArchived := plan.Archived.ValueBool()
	haveArchived := st.Archived.ValueBool()
	if haveArchived && !wantArchived {
		d, err := r.client.UnarchiveDefinition(ctx, r.kind, id, version)
		if !step(d, err, "Unarchiving the "+r.typeName+" failed") {
			return
		}
	}
	patch := map[string]any{}
	if plan.Name.ValueString() != st.Name.ValueString() {
		patch["name"] = plan.Name.ValueString()
	}
	if r.hasParent() && !plan.ParentID.Equal(st.ParentID) {
		if p := nullableString(plan.ParentID); p != nil {
			patch["parent_id"] = *p
		} else {
			patch["parent_id"] = nil
		}
	}
	if len(patch) > 0 {
		d, err := r.client.UpdateDefinition(ctx, r.kind, id, version, patch)
		if !step(d, err, "Updating the "+r.typeName+" failed") {
			return
		}
	}
	if !haveArchived && wantArchived {
		d, err := r.client.ArchiveDefinition(ctx, r.kind, id, version)
		if !step(d, err, "Archiving the "+r.typeName+" failed") {
			return
		}
	}
	if cur == nil {
		// Only reassign_to changed: nothing to send, but the computed values must be known again.
		d, err := r.client.GetDefinition(ctx, r.kind, id)
		if !step(d, err, "Reading the "+r.typeName+" failed") {
			return
		}
	}
	resp.Diagnostics.Append(r.write(ctx, &resp.State, *cur, plan.ReassignTo)...)
}

func (r *definitionResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	st, diags := r.read(ctx, req.State)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	_, err := r.client.DeleteDefinition(ctx, r.kind, st.ID.ValueString(), st.Version.ValueInt64(), st.ReassignTo.ValueString())
	switch {
	case err == nil || client.IsNotFound(err):
	case client.HasCode(err, client.CodeDefinitionInUse):
		resp.Diagnostics.AddError("The "+r.typeName+" is still assigned",
			err.Error()+" Archive it instead (archived = true) to keep history, or set reassign_to to an active "+r.typeName+
				" that the assignments should move to, apply that, then destroy.")
	case client.IsVersionConflict(err):
		resp.Diagnostics.AddError("Deleting the "+r.typeName+" failed", versionConflictDetail(err))
	default:
		resp.Diagnostics.AddError("Deleting the "+r.typeName+" failed", err.Error())
	}
}

func (r *definitionResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !client.ValidID(req.ID) {
		resp.Diagnostics.AddError("Invalid import identifier",
			fmt.Sprintf("Expected the Ledger identifier of the %s (a 26-character upper-case ULID), got %q.", r.typeName, req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
