package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
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
	_ datasource.DataSource              = (*userDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*userDataSource)(nil)
	_ datasource.DataSource              = (*usersDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*usersDataSource)(nil)
)

var userAttrTypes = map[string]attr.Type{
	"id": types.StringType, "username": types.StringType, "display_name": types.StringType, "role": types.StringType,
	"disabled": types.BoolType, "disabled_at": types.StringType, "version": types.Int64Type,
	"created_at": types.StringType, "updated_at": types.StringType,
}

func userValue(u client.User) (attr.Value, diag.Diagnostics) {
	return types.ObjectValue(userAttrTypes, map[string]attr.Value{
		"id": types.StringValue(u.ID), "username": types.StringValue(u.Username), "display_name": strOrNull(u.DisplayName),
		"role": types.StringValue(u.Role), "disabled": types.BoolValue(u.Disabled), "disabled_at": strOrNull(u.DisabledAt),
		"version": types.Int64Value(u.Version), "created_at": types.StringValue(u.CreatedAt), "updated_at": types.StringValue(u.UpdatedAt),
	})
}

func userItemSchema() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"id":           schema.StringAttribute{Computed: true, MarkdownDescription: "The Ledger identifier."},
		"username":     schema.StringAttribute{Computed: true, MarkdownDescription: "The sign-in name."},
		"display_name": schema.StringAttribute{Computed: true, MarkdownDescription: "The display name, or null."},
		"role":         schema.StringAttribute{Computed: true, MarkdownDescription: "`administrator` or `read-only`."},
		"disabled":     schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the user is disabled."},
		"disabled_at":  schema.StringAttribute{Computed: true, MarkdownDescription: "When it was disabled (RFC 3339, UTC), or null."},
		"version":      schema.Int64Attribute{Computed: true, MarkdownDescription: "The optimistic-concurrency version."},
		"created_at":   schema.StringAttribute{Computed: true, MarkdownDescription: "When it was created (RFC 3339, UTC)."},
		"updated_at":   schema.StringAttribute{Computed: true, MarkdownDescription: "When it last changed (RFC 3339, UTC)."},
	}
}

// ---- ledger_user ----

type userDataSource struct{ client *client.Client }

// NewUserDataSource is the data source constructor.
func NewUserDataSource() datasource.DataSource { return &userDataSource{} }

func (d *userDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user"
}

func (d *userDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := userItemSchema()
	attrs["id"] = schema.StringAttribute{Optional: true, Computed: true,
		MarkdownDescription: "The Ledger identifier. Set `id` or `username`, not both.",
		Validators:          []validator.String{idValidator(), stringvalidator.ExactlyOneOf(path.MatchRoot("id"), path.MatchRoot("username"))}}
	attrs["username"] = schema.StringAttribute{Optional: true, Computed: true,
		MarkdownDescription: "The exact sign-in name. Disabled users are found too. Set `id` or `username`, not both."}
	resp.Schema = schema.Schema{MarkdownDescription: "Reads one Ledger user by identifier or by username. " + userGrantNote, Attributes: attrs}
}

func (d *userDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *userDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var id, name types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("id"), &id)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("username"), &name)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var found client.User
	if !id.IsNull() {
		u, err := d.client.GetUser(ctx, id.ValueString())
		if err != nil {
			if client.IsNotFound(err) {
				resp.Diagnostics.AddAttributeError(path.Root("id"), "No such user", "Ledger has no user with that identifier.")
				return
			}
			resp.Diagnostics.AddError("Reading the user failed", err.Error())
			return
		}
		found = u
	} else {
		items, err := d.client.ListUsers(ctx, client.UserFilter{UsernamePrefix: name.ValueString()})
		if err != nil {
			resp.Diagnostics.AddError("Looking up the user failed", err.Error())
			return
		}
		var matches []client.User
		for _, it := range items {
			if it.Username == name.ValueString() {
				matches = append(matches, it)
			}
		}
		switch len(matches) {
		case 0:
			resp.Diagnostics.AddAttributeError(path.Root("username"), "No such user", "Ledger has no user with that username.")
			return
		case 1:
			found = matches[0]
		default:
			resp.Diagnostics.AddAttributeError(path.Root("username"), "Ambiguous username", fmt.Sprintf("%d users have that username; look one up by id.", len(matches)))
			return
		}
	}
	set := func(n string, v any) { resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root(n), v)...) }
	set("id", found.ID)
	set("username", found.Username)
	set("display_name", strOrNull(found.DisplayName))
	set("role", found.Role)
	set("disabled", found.Disabled)
	set("disabled_at", strOrNull(found.DisabledAt))
	set("version", found.Version)
	set("created_at", found.CreatedAt)
	set("updated_at", found.UpdatedAt)
}

// ---- ledger_users ----

type usersDataSource struct{ client *client.Client }

// NewUsersDataSource is the data source constructor.
func NewUsersDataSource() datasource.DataSource { return &usersDataSource{} }

func (d *usersDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_users"
}

func (d *usersDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists Ledger users, in identifier order, disabled ones included unless filtered. " + userGrantNote,
		Attributes: map[string]schema.Attribute{
			"roles": schema.ListAttribute{Optional: true, ElementType: types.StringType,
				Validators:          []validator.List{listvalidator.ValueStringsAre(roleValidator())},
				MarkdownDescription: "Only users with one of these roles (`administrator`, `read-only`)."},
			"disabled": schema.BoolAttribute{Optional: true,
				MarkdownDescription: "Only disabled (`true`) or only enabled (`false`) users. Both are listed when unset."},
			"username_prefix": schema.StringAttribute{Optional: true, Validators: []validator.String{stringvalidator.LengthAtLeast(1)},
				MarkdownDescription: "Only users whose username starts with this text."},
			"items": schema.ListNestedAttribute{Computed: true, MarkdownDescription: "The users.",
				NestedObject: schema.NestedAttributeObject{Attributes: userItemSchema()}},
		},
	}
}

func (d *usersDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *usersDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var roles types.List
	var disabled types.Bool
	var prefix types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("roles"), &roles)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("disabled"), &disabled)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("username_prefix"), &prefix)...)
	if resp.Diagnostics.HasError() {
		return
	}
	f := client.UserFilter{UsernamePrefix: prefix.ValueString()}
	if !roles.IsNull() {
		resp.Diagnostics.Append(roles.ElementsAs(ctx, &f.Roles, false)...)
	}
	if !disabled.IsNull() {
		b := disabled.ValueBool()
		f.Disabled = &b
	}
	if resp.Diagnostics.HasError() {
		return
	}
	items, err := d.client.ListUsers(ctx, f)
	if err != nil {
		resp.Diagnostics.AddError("Listing users failed", err.Error())
		return
	}
	vals := make([]attr.Value, 0, len(items))
	for _, it := range items {
		v, diags := userValue(it)
		resp.Diagnostics.Append(diags...)
		vals = append(vals, v)
	}
	if resp.Diagnostics.HasError() {
		return
	}
	list, diags := types.ListValue(types.ObjectType{AttrTypes: userAttrTypes}, vals)
	resp.Diagnostics.Append(diags...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("items"), list)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("roles"), roles)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("disabled"), disabled)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("username_prefix"), prefix)...)
}
