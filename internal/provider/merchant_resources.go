package provider

import (
	"context"
	"fmt"

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

var (
	_ resource.Resource                = (*merchantResource)(nil)
	_ resource.ResourceWithConfigure   = (*merchantResource)(nil)
	_ resource.ResourceWithImportState = (*merchantResource)(nil)
	_ resource.Resource                = (*merchantAliasResource)(nil)
	_ resource.ResourceWithConfigure   = (*merchantAliasResource)(nil)
	_ resource.ResourceWithImportState = (*merchantAliasResource)(nil)
)

const merchantGrantNote = "Merchant routes need an API token with the administrator-only `merchants:read` and `merchants:write` grants (merchant names derive from bank text)."

// ---- ledger_merchant ----

type merchantResource struct{ client *client.Client }

// NewMerchantResource is the resource constructor.
func NewMerchantResource() resource.Resource { return &merchantResource{} }

type merchantModel struct {
	ID          types.String `tfsdk:"id"`
	DisplayName types.String `tfsdk:"display_name"`
	Archived    types.Bool   `tfsdk:"archived"`
	MergedInto  types.String `tfsdk:"merged_into"`
	Version     types.Int64  `tfsdk:"version"`
	ArchivedAt  types.String `tfsdk:"archived_at"`
	CreatedAt   types.String `tfsdk:"created_at"`
	UpdatedAt   types.String `tfsdk:"updated_at"`
}

func (r *merchantResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_merchant"
}

func (r *merchantResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A Ledger merchant record: a stable identifier, a display name and the alias names transactions are matched by. " +
			"Categories and labels are assigned to it with `ledger_category_assignment` and `ledger_label_assignment` (`object_type = \"merchant\"`). " +
			"Creating one makes the display name its first alias; renaming adds the new name as an alias. Merging duplicates is done in Ledger itself, not here. " +
			merchantGrantNote + "\n\nImport with the identifier: `terraform import ledger_merchant.example <id>`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, MarkdownDescription: "The Ledger identifier (a ULID). It never changes, so a rename keeps every assignment.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"display_name": schema.StringAttribute{Required: true, Validators: []validator.String{merchantNameValidator()},
				MarkdownDescription: "The name shown for the merchant, 1 to 200 characters. Ledger trims it, collapses white space and normalises Unicode (NFC), so write it that way. " +
					"A name that another merchant already has as an alias is refused (`merchant_alias_taken`): merge the two in Ledger instead."},
			"archived": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false),
				MarkdownDescription: "Whether the merchant is archived. An archived merchant keeps its aliases and every assignment, and takes nothing new by identifier. " +
					"Archive instead of destroying when anything refers to it. Defaults to `false`."},
			"merged_into": schema.StringAttribute{Computed: true, MarkdownDescription: "The identifier of the merchant this one was merged into (in Ledger), or null. A merged merchant is archived and cannot be unarchived."},
			"version":     schema.Int64Attribute{Computed: true, MarkdownDescription: "The optimistic-concurrency version. Changes are sent against the version Terraform last read; a stale one fails instead of overwriting."},
			"archived_at": schema.StringAttribute{Computed: true, MarkdownDescription: "When it was archived (RFC 3339, UTC), or null."},
			"created_at": schema.StringAttribute{Computed: true, MarkdownDescription: "When it was created (RFC 3339, UTC).",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"updated_at": schema.StringAttribute{Computed: true, MarkdownDescription: "When it last changed (RFC 3339, UTC)."},
		},
	}
}

func (r *merchantResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func merchantModelFrom(m client.Merchant) merchantModel {
	return merchantModel{ID: types.StringValue(m.ID), DisplayName: types.StringValue(m.DisplayName), Archived: types.BoolValue(m.Archived),
		MergedInto: strOrNull(m.MergedInto), Version: types.Int64Value(m.Version), ArchivedAt: strOrNull(m.ArchivedAt),
		CreatedAt: types.StringValue(m.CreatedAt), UpdatedAt: types.StringValue(m.UpdatedAt)}
}

// withCurrentVersion runs op with version. A merchant's version also moves when an alias is added or removed, and
// ledger_merchant_alias does that in the same run (after this resource was read), so a stale version is retried once
// with the current one, but only when the attributes this resource manages are what state says. A change to those made
// outside Terraform is still a conflict (never an overwrite, ADR-0004 item 3).
func (r *merchantResource) withCurrentVersion(ctx context.Context, st merchantModel, version int64, op func(version int64) (client.Merchant, error)) (client.Merchant, error) {
	m, err := op(version)
	if err == nil || !client.IsVersionConflict(err) {
		return m, err
	}
	cur, gerr := r.client.GetMerchant(ctx, st.ID.ValueString())
	if gerr != nil || cur.DisplayName != st.DisplayName.ValueString() || cur.Archived != st.Archived.ValueBool() || cur.Version == version {
		return m, err
	}
	return op(cur.Version)
}

func (r *merchantResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan merchantModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	m, err := r.client.CreateMerchant(ctx, plan.DisplayName.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Creating the merchant failed", err.Error())
		return
	}
	// Track the created record first so a failed archive step leaves it in state (Terraform then taints it).
	resp.Diagnostics.Append(resp.State.Set(ctx, merchantModelFrom(m))...)
	if plan.Archived.ValueBool() {
		a, err := r.client.ArchiveMerchant(ctx, m.ID, m.Version)
		if err != nil {
			resp.Diagnostics.AddError("Archiving the new merchant failed", "It was created but is not archived. "+err.Error())
			return
		}
		resp.Diagnostics.Append(resp.State.Set(ctx, merchantModelFrom(a))...)
	}
}

func (r *merchantResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var st merchantModel
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	m, err := r.client.GetMerchant(ctx, st.ID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Reading the merchant failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, merchantModelFrom(m))...)
}

func (r *merchantResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, st merchantModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, version := st.ID.ValueString(), st.Version.ValueInt64()
	var cur *client.Merchant
	// Only the first call can meet an alias-made version bump; later ones use the version the previous answer gave.
	first := true
	run := func(op func(version int64) (client.Merchant, error)) (client.Merchant, error) {
		if first {
			first = false
			return r.withCurrentVersion(ctx, st, version, op)
		}
		return op(version)
	}
	step := func(m client.Merchant, err error, what string) bool {
		if err != nil {
			detail := err.Error()
			if client.IsVersionConflict(err) {
				detail = versionConflictDetail(err)
			}
			resp.Diagnostics.AddError(what, detail)
			return false
		}
		cur, version = &m, m.Version
		return true
	}
	wantArchived, haveArchived := plan.Archived.ValueBool(), st.Archived.ValueBool()
	if haveArchived && !wantArchived {
		m, err := run(func(v int64) (client.Merchant, error) { return r.client.UnarchiveMerchant(ctx, id, v) })
		if !step(m, err, "Unarchiving the merchant failed") {
			return
		}
	}
	if plan.DisplayName.ValueString() != st.DisplayName.ValueString() {
		m, err := run(func(v int64) (client.Merchant, error) {
			return r.client.RenameMerchant(ctx, id, v, plan.DisplayName.ValueString())
		})
		if !step(m, err, "Renaming the merchant failed") {
			return
		}
	}
	if !haveArchived && wantArchived {
		m, err := run(func(v int64) (client.Merchant, error) { return r.client.ArchiveMerchant(ctx, id, v) })
		if !step(m, err, "Archiving the merchant failed") {
			return
		}
	}
	if cur == nil {
		m, err := r.client.GetMerchant(ctx, id)
		if !step(m, err, "Reading the merchant failed") {
			return
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, merchantModelFrom(*cur))...)
}

func (r *merchantResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var st merchantModel
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	_, err := r.withCurrentVersion(ctx, st, st.Version.ValueInt64(), func(v int64) (client.Merchant, error) {
		return client.Merchant{}, r.client.DeleteMerchant(ctx, st.ID.ValueString(), v)
	})
	switch {
	case err == nil || client.IsNotFound(err):
	case client.HasCode(err, client.CodeMerchantInUse):
		resp.Diagnostics.AddError("The merchant is still in use",
			err.Error()+" Decisions, assignments or merged merchants refer to it. Archive it instead (archived = true) to keep history.")
	case client.IsVersionConflict(err):
		resp.Diagnostics.AddError("Deleting the merchant failed", versionConflictDetail(err))
	default:
		resp.Diagnostics.AddError("Deleting the merchant failed", err.Error())
	}
}

func (r *merchantResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !client.ValidID(req.ID) {
		resp.Diagnostics.AddError("Invalid import identifier",
			fmt.Sprintf("Expected the Ledger identifier of the merchant (a 26-character upper-case ULID), got %q.", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

// ---- ledger_merchant_alias ----

type merchantAliasResource struct{ client *client.Client }

// NewMerchantAliasResource is the resource constructor.
func NewMerchantAliasResource() resource.Resource { return &merchantAliasResource{} }

type merchantAliasModel struct {
	ID         types.String `tfsdk:"id"`
	MerchantID types.String `tfsdk:"merchant_id"`
	Name       types.String `tfsdk:"name"`
	AliasID    types.String `tfsdk:"alias_id"`
	Key        types.String `tfsdk:"key"`
	Origin     types.String `tfsdk:"origin"`
}

func (r *merchantAliasResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_merchant_alias"
}

func (r *merchantAliasResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A name that Ledger matches transactions to a merchant by. A name belongs to one merchant at most (compared ignoring case and white space), " +
			"so adding one that another merchant has fails with `merchant_alias_taken`. If the merchant already has the name (every merchant has its display name), " +
			"this resource takes it over, and destroying the resource removes that alias. An alias cannot be changed: a new name replaces the resource. " +
			merchantGrantNote + "\n\nImport with `<merchant_id>/<alias_id>`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, MarkdownDescription: "`<merchant_id>/<alias_id>`.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"merchant_id": schema.StringAttribute{Required: true, MarkdownDescription: "The identifier of the merchant. Changing it replaces the resource.", PlanModifiers: replaceOnChange,
				Validators: []validator.String{idValidator()}},
			"name": schema.StringAttribute{Required: true, MarkdownDescription: "The alias name, 1 to 200 characters, as Ledger stores it (trimmed, white space collapsed). Changing it replaces the resource.",
				PlanModifiers: replaceOnChange, Validators: []validator.String{merchantNameValidator()}},
			"alias_id": schema.StringAttribute{Computed: true, MarkdownDescription: "The alias identifier.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"key":      schema.StringAttribute{Computed: true, MarkdownDescription: "The name as matched: Unicode NFC, white space collapsed, lower case."},
			"origin":   schema.StringAttribute{Computed: true, MarkdownDescription: "How the alias came to be: `user` (the API), `decision` (a confirmation or correction named it) or `upgrade`."},
		},
	}
}

func (r *merchantAliasResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (m *merchantAliasModel) fill(merchantID string, a client.MerchantAlias) {
	m.ID = types.StringValue(merchantID + "/" + a.ID)
	m.MerchantID = types.StringValue(merchantID)
	m.Name = types.StringValue(a.Name)
	m.AliasID = types.StringValue(a.ID)
	m.Key = types.StringValue(a.Key)
	m.Origin = types.StringValue(a.Origin)
}

func (r *merchantAliasResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan merchantAliasModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, name := plan.MerchantID.ValueString(), plan.Name.ValueString()
	m, err := r.client.AddMerchantAlias(ctx, id, name)
	if err != nil {
		summary := "Adding the alias failed"
		if client.HasCode(err, client.CodeMerchantAliasTaken) {
			summary = "The alias belongs to another merchant"
		}
		resp.Diagnostics.AddError(summary, err.Error())
		return
	}
	for _, a := range m.Aliases {
		if a.Name == name {
			var out merchantAliasModel
			out.fill(id, a)
			resp.Diagnostics.Append(resp.State.Set(ctx, &out)...)
			return
		}
	}
	resp.Diagnostics.AddError("The alias is not on the merchant", "Ledger accepted the alias but the merchant does not list it under that name. This is unexpected; read the merchant again.")
}

// Update is never called: every writable attribute forces replacement.
func (r *merchantAliasResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Unexpected update", "A merchant alias has no updatable attribute. This is a provider bug.")
}

func (r *merchantAliasResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var st merchantAliasModel
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	m, err := r.client.GetMerchant(ctx, st.MerchantID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Reading the merchant failed", err.Error())
		return
	}
	for _, a := range m.Aliases {
		if a.ID == st.AliasID.ValueString() {
			st.fill(m.ID, a)
			resp.Diagnostics.Append(resp.State.Set(ctx, &st)...)
			return
		}
	}
	resp.State.RemoveResource(ctx)
}

func (r *merchantAliasResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var st merchantAliasModel
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if _, err := r.client.RemoveMerchantAlias(ctx, st.MerchantID.ValueString(), st.AliasID.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Removing the alias failed", err.Error())
	}
}

func (r *merchantAliasResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts, err := splitImportID(req.ID, 2, "<merchant_id>/<alias_id>")
	if err != nil {
		resp.Diagnostics.AddError("Invalid import identifier", err.Error())
		return
	}
	if !client.ValidID(parts[0]) || !client.ValidID(parts[1]) {
		resp.Diagnostics.AddError("Invalid import identifier", "Both parts must be Ledger identifiers (26-character upper-case ULIDs).")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("merchant_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("alias_id"), parts[1])...)
}
