package provider

import (
	"context"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aurimasniekis/terraform-provider-ledger/internal/client"
)

var (
	_ resource.Resource              = (*userPasswordResource)(nil)
	_ resource.ResourceWithConfigure = (*userPasswordResource)(nil)
)

// userPasswordResource sets a user's password from a write-only attribute: the value is read from the configuration
// during create and update and is never part of the plan or the state (Terraform 1.11 or later).
type userPasswordResource struct{ client *client.Client }

// NewUserPasswordResource is the resource constructor.
func NewUserPasswordResource() resource.Resource { return &userPasswordResource{} }

type userPasswordModel struct {
	ID                types.String `tfsdk:"id"`
	UserID            types.String `tfsdk:"user_id"`
	PasswordWO        types.String `tfsdk:"password_wo"`
	PasswordWOVersion types.Int64  `tfsdk:"password_wo_version"`
	SessionsEnded     types.Int64  `tfsdk:"sessions_ended"`
}

func (r *userPasswordResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user_password"
}

func (r *userPasswordResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Sets the password of a Ledger user. The password is **write-only**: it is read from the configuration when the password is set, " +
			"and is never stored in the plan or the state (needs Terraform 1.11 or later). Because it is not stored, Terraform cannot see a password changed elsewhere; " +
			"to set a new one, change the value **and** raise `password_wo_version`.\n\n" +
			"Setting a password ends every session of that user and revokes their device cookies. " +
			"**With an API token this needs the root token of the root account**, even for the token owner's own password; any other token is refused with `root_token_required`. " +
			"Ledger has no operation to remove a password, so destroying this resource only forgets it: the password stays set. Disable the user (destroy `ledger_user`) to stop sign-in. " +
			"Ledger enforces a minimum length (configured on the server, in bytes) and at most 1024 bytes, with no composition rules.\n\n" +
			"Not importable: there is nothing to read back.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, MarkdownDescription: "The identifier of the user (the same as `user_id`).",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"user_id": schema.StringAttribute{Required: true, Validators: []validator.String{idValidator()},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				MarkdownDescription: "The Ledger identifier of the user. Changing it sets the password of the other user (the resource is replaced)."},
			"password_wo": schema.StringAttribute{Required: true, WriteOnly: true, Sensitive: true,
				MarkdownDescription: "The new password. Write-only: never stored in the plan or the state, and not shown. Supply it from an ephemeral source, not a literal checked into version control. " +
					"A change to this value alone does nothing; raise `password_wo_version` to apply it."},
			"password_wo_version": schema.Int64Attribute{Optional: true, Validators: []validator.Int64{int64validator.AtLeast(0)},
				MarkdownDescription: "A number you raise to set the password again. Terraform stores it (it is not secret) and calls Ledger when it changes. " +
					"Optional, but without it the password can only be set again by replacing the resource. Removing it after it was set counts as a change: the password is sent again, which ends that user's sessions."},
			"sessions_ended": schema.Int64Attribute{Computed: true,
				MarkdownDescription: "How many sessions the last password change ended."},
		},
	}
}

func (r *userPasswordResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

// set reads the write-only password from the configuration, sends it and records the outcome. The value is never put in
// a diagnostic: Ledger's answers do not carry it, and nothing here formats it.
func (r *userPasswordResource) set(ctx context.Context, config tfsdk.Config, plan userPasswordModel, diags *diag.Diagnostics) (userPasswordModel, bool) {
	var pw types.String
	diags.Append(config.GetAttribute(ctx, path.Root("password_wo"), &pw)...)
	if diags.HasError() {
		return plan, false
	}
	if pw.IsNull() || pw.IsUnknown() || pw.ValueString() == "" {
		diags.AddAttributeError(path.Root("password_wo"), "A password is required", "password_wo must be set to a non-empty value when the password is set.")
		return plan, false
	}
	n, err := r.client.SetUserPassword(ctx, plan.UserID.ValueString(), client.Secret(pw.ValueString()))
	if err != nil {
		summary := "Setting the password failed"
		detail := err.Error()
		if client.HasCode(err, client.CodeRootTokenRequired) {
			detail += " Setting a password with an API token needs the root token of the root account; configure the provider with that token for this change."
		}
		diags.AddError(summary, detail)
		return plan, false
	}
	plan.ID = plan.UserID
	plan.SessionsEnded = types.Int64Value(n)
	return plan, true
}

func (r *userPasswordResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan userPasswordModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	out, ok := r.set(ctx, req.Config, plan, &resp.Diagnostics)
	if !ok {
		return
	}
	out.PasswordWO = types.StringNull() // write-only: never in state
	resp.Diagnostics.Append(resp.State.Set(ctx, out)...)
}

// Read keeps the state as it is when the user still exists: the password cannot be read back. A user that is gone
// removes the resource.
func (r *userPasswordResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var st userPasswordModel
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if _, err := r.client.GetUser(ctx, st.UserID.ValueString()); err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Reading the user failed", err.Error())
	}
}

func (r *userPasswordResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, st userPasswordModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if plan.PasswordWOVersion.Equal(st.PasswordWOVersion) {
		// Nothing asks for a new password (only the write-only value may differ, and it is not stored).
		plan.SessionsEnded = st.SessionsEnded
		plan.PasswordWO = types.StringNull()
		resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
		return
	}
	out, ok := r.set(ctx, req.Config, plan, &resp.Diagnostics)
	if !ok {
		return
	}
	out.PasswordWO = types.StringNull()
	resp.Diagnostics.Append(resp.State.Set(ctx, out)...)
}

// Delete only forgets: Ledger has no operation to remove a password.
func (r *userPasswordResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}
