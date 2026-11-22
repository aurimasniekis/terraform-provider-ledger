package provider

import (
	"context"
	"fmt"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
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
	_ resource.Resource                = (*userResource)(nil)
	_ resource.ResourceWithConfigure   = (*userResource)(nil)
	_ resource.ResourceWithImportState = (*userResource)(nil)
)

const userGrantNote = "User routes need an API token with the `users:read` and `users:write` grants. " +
	"Creating or promoting an administrator, re-enabling a disabled administrator, and changing the root (first) account need the **root token** of the root account; " +
	"any other token is refused with `root_token_required`."

var usernameRegexp = regexp.MustCompile("^[a-z0-9][a-z0-9._-]*$")

func usernameValidators() []validator.String {
	return []validator.String{
		stringvalidator.LengthBetween(1, 64),
		stringvalidator.RegexMatches(usernameRegexp, "must start with a lower-case letter or digit and contain only lower-case letters, digits, '.', '_' and '-'"),
	}
}

func roleValidator() validator.String {
	return stringvalidator.OneOf(client.RoleAdministrator, client.RoleReadOnly)
}

type userResource struct{ client *client.Client }

// NewUserResource is the resource constructor.
func NewUserResource() resource.Resource { return &userResource{} }

type userModel struct {
	ID          types.String `tfsdk:"id"`
	Username    types.String `tfsdk:"username"`
	DisplayName types.String `tfsdk:"display_name"`
	Role        types.String `tfsdk:"role"`
	Disabled    types.Bool   `tfsdk:"disabled"`
	DisabledAt  types.String `tfsdk:"disabled_at"`
	Version     types.Int64  `tfsdk:"version"`
	CreatedAt   types.String `tfsdk:"created_at"`
	UpdatedAt   types.String `tfsdk:"updated_at"`
}

func userModelFrom(u client.User) userModel {
	return userModel{ID: types.StringValue(u.ID), Username: types.StringValue(u.Username), DisplayName: strOrNull(u.DisplayName),
		Role: types.StringValue(u.Role), Disabled: types.BoolValue(u.Disabled), DisabledAt: strOrNull(u.DisabledAt),
		Version: types.Int64Value(u.Version), CreatedAt: types.StringValue(u.CreatedAt), UpdatedAt: types.StringValue(u.UpdatedAt)}
}

// userHint adds the advice a person can act on to the API errors users meet.
func userHint(err error) string {
	switch {
	case client.IsVersionConflict(err):
		return versionConflictDetail(err)
	case client.HasCode(err, client.CodeRootTokenRequired):
		return err.Error() + " This operation needs the root token of the root account (create it on the Ledger host with `ledger token create --create administrator --root`); " +
			"configure the provider with that token for this change."
	case client.HasCode(err, client.CodeConflict):
		return err.Error() + " Ledger refuses a change that leaves no enabled administrator, and a username that is already taken."
	}
	return err.Error()
}

func (r *userResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user"
}

func (r *userResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A Ledger user account: a username, an optional display name and a role. " +
			"**Ledger never deletes users**: destroying this resource **disables** the user (their sessions and tokens stop working, audit history and token ownership stay) " +
			"and removes it from state. Credentials are separate: set a password with `ledger_user_password` (write-only). " +
			userGrantNote + "\n\nImport with the identifier: `terraform import ledger_user.example <id>`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, MarkdownDescription: "The Ledger identifier (a ULID).",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"username": schema.StringAttribute{Required: true, Validators: usernameValidators(),
				MarkdownDescription: "The sign-in name, 1 to 64 characters: lower-case letters, digits, `.`, `_` and `-`, starting with a letter or digit. " +
					"It can be renamed in place; renaming the root account needs the root token. A name that is taken is refused."},
			"display_name": schema.StringAttribute{Optional: true, Validators: []validator.String{stringvalidator.LengthBetween(1, 100)},
				MarkdownDescription: "An optional display name, 1 to 100 characters. Remove the attribute to clear it."},
			"role": schema.StringAttribute{Required: true, Validators: []validator.String{roleValidator()},
				MarkdownDescription: "`administrator` (every grant) or `read-only` (read on non-administrative resources). " +
					"Making a user an administrator needs the root token; Ledger refuses a change that would leave no enabled administrator."},
			"disabled": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false),
				MarkdownDescription: "Whether the user is disabled. Defaults to `false`. Re-enabling a disabled administrator needs the root token."},
			"disabled_at": schema.StringAttribute{Computed: true, MarkdownDescription: "When the user was disabled (RFC 3339, UTC), or null."},
			"version":     schema.Int64Attribute{Computed: true, MarkdownDescription: "The optimistic-concurrency version. Changes are sent against the version Terraform last read; a stale one fails instead of overwriting."},
			"created_at": schema.StringAttribute{Computed: true, MarkdownDescription: "When the user was created (RFC 3339, UTC).",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"updated_at": schema.StringAttribute{Computed: true, MarkdownDescription: "When it last changed (RFC 3339, UTC)."},
		},
	}
}

func (r *userResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (r *userResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan userModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	u, err := r.client.CreateUser(ctx, plan.Username.ValueString(), plan.Role.ValueString(), nullableString(plan.DisplayName))
	if err != nil {
		resp.Diagnostics.AddError("Creating the user failed", userHint(err))
		return
	}
	// Track the created user first so a failed disable step leaves it in state (Terraform then taints it).
	resp.Diagnostics.Append(resp.State.Set(ctx, userModelFrom(u))...)
	if plan.Disabled.ValueBool() {
		yes := true
		d, err := r.client.UpdateUser(ctx, u.ID, u.Version, client.UserPatch{Disabled: &yes})
		if err != nil {
			resp.Diagnostics.AddError("Disabling the new user failed", "It was created but is not disabled. "+userHint(err))
			return
		}
		resp.Diagnostics.Append(resp.State.Set(ctx, userModelFrom(d))...)
	}
}

func (r *userResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var st userModel
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	u, err := r.client.GetUser(ctx, st.ID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Reading the user failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, userModelFrom(u))...)
}

func (r *userResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, st userModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var patch client.UserPatch
	if !plan.Username.Equal(st.Username) {
		v := plan.Username.ValueString()
		patch.Username = &v
	}
	if !plan.DisplayName.Equal(st.DisplayName) {
		patch.DisplayName = new(*string)
		*patch.DisplayName = nullableString(plan.DisplayName)
	}
	if !plan.Role.Equal(st.Role) {
		v := plan.Role.ValueString()
		patch.Role = &v
	}
	if !plan.Disabled.Equal(st.Disabled) {
		v := plan.Disabled.ValueBool()
		patch.Disabled = &v
	}
	u, err := r.client.UpdateUser(ctx, st.ID.ValueString(), st.Version.ValueInt64(), patch)
	if err != nil {
		resp.Diagnostics.AddError("Updating the user failed", userHint(err))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, userModelFrom(u))...)
}

// Delete disables the user: Ledger has no user delete (it keeps audit history and token ownership).
func (r *userResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var st userModel
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	cur, err := r.client.GetUser(ctx, st.ID.ValueString())
	switch {
	case client.IsNotFound(err):
		return
	case err != nil:
		resp.Diagnostics.AddError("Reading the user before disabling it failed", err.Error())
		return
	case cur.Disabled:
		return
	}
	// The version just read is used: the intent is "disabled", whatever else changed meanwhile.
	yes := true
	if _, err := r.client.UpdateUser(ctx, cur.ID, cur.Version, client.UserPatch{Disabled: &yes}); err != nil {
		resp.Diagnostics.AddError("Disabling the user failed",
			"Ledger has no user delete, so destroying a user disables it. "+userHint(err))
	}
}

func (r *userResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !client.ValidID(req.ID) {
		resp.Diagnostics.AddError("Invalid import identifier",
			fmt.Sprintf("Expected the Ledger identifier of the user (a 26-character upper-case ULID), got %q.", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
