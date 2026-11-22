package provider

import (
	"context"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aurimasniekis/terraform-provider-ledger/internal/client"
)

var (
	_ resource.Resource                   = (*apiTokenResource)(nil)
	_ resource.ResourceWithConfigure      = (*apiTokenResource)(nil)
	_ resource.ResourceWithValidateConfig = (*apiTokenResource)(nil)
)

// apiTokenResource creates an API token. This is the one place where the provider keeps a secret in state (ADR-0015):
// Ledger generates the token and returns it once, so it is held in a sensitive computed attribute. A token cannot be
// changed, so every configurable attribute replaces it.
type apiTokenResource struct{ client *client.Client }

// NewAPITokenResource is the resource constructor.
func NewAPITokenResource() resource.Resource { return &apiTokenResource{} }

type apiTokenModel struct {
	ID                 types.String `tfsdk:"id"`
	Name               types.String `tfsdk:"name"`
	Grants             types.Set    `tfsdk:"grants"`
	Root               types.Bool   `tfsdk:"root"`
	ExpiresAt          types.String `tfsdk:"expires_at"`
	Secret             types.String `tfsdk:"secret"`
	Warning            types.String `tfsdk:"warning"`
	OwnerID            types.String `tfsdk:"owner_id"`
	Hint               types.String `tfsdk:"hint"`
	Prefix             types.String `tfsdk:"prefix"`
	LastChars          types.String `tfsdk:"last_chars"`
	Status             types.String `tfsdk:"status"`
	EffectiveExpiresAt types.String `tfsdk:"effective_expires_at"`
	CreatedAt          types.String `tfsdk:"created_at"`
	LastUsedAt         types.String `tfsdk:"last_used_at"`
	RevokedAt          types.String `tfsdk:"revoked_at"`
	ParentTokenID      types.String `tfsdk:"parent_token_id"`
}

// applyMetadata copies what Ledger serves about the token into the model. It never touches the secret.
func (m *apiTokenModel) applyMetadata(t client.APIToken) diag.Diagnostics {
	g, diags := grantsSet(t)
	m.ID = types.StringValue(t.ID)
	m.Name = types.StringValue(t.Name)
	m.Grants = g
	m.Root = types.BoolValue(t.Root)
	m.OwnerID = types.StringValue(t.OwnerID)
	m.Hint = types.StringValue(t.Hint)
	m.Prefix = types.StringValue(t.Prefix)
	m.LastChars = types.StringValue(t.LastChars)
	m.Status = types.StringValue(t.Status)
	m.EffectiveExpiresAt = strOrNull(t.ExpiresAt)
	m.CreatedAt = types.StringValue(t.CreatedAt)
	m.LastUsedAt = strOrNull(t.LastUsedAt)
	m.RevokedAt = strOrNull(t.RevokedAt)
	m.ParentTokenID = strOrNull(t.ParentTokenID)
	return diags
}

func (r *apiTokenResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_api_token"
}

func rfc3339Validator() validator.String {
	return rfc3339Check{}
}

type rfc3339Check struct{}

func (rfc3339Check) Description(context.Context) string { return "must be an RFC 3339 timestamp" }
func (c rfc3339Check) MarkdownDescription(ctx context.Context) string {
	return c.Description(ctx)
}
func (rfc3339Check) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if _, err := time.Parse(time.RFC3339, req.ConfigValue.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid timestamp", "expires_at must be an RFC 3339 timestamp such as 2030-01-31T00:00:00Z.")
	}
}

func (r *apiTokenResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "An API token of Ledger. Ledger generates the token and shows it **once**, when it is created; this resource keeps it in the `secret` attribute.\n\n" +
			"~> **The token is stored in the Terraform state.** This is the one secret the provider keeps in state (every other secret is write-only), because Ledger, not you, generates it. " +
			"Encrypt the state, restrict who can read it, prefer short-lived tokens (`expires_at`) and narrow `grants`, and treat the state like the token. `secret` is marked sensitive, which hides it from plan output only. " +
			"Never print it with `output` unless that output is also `sensitive = true`.\n\n" +
			"A token cannot be changed: changing any argument **replaces** it (the old one is revoked, then a new one is created). " +
			"Destroying the resource **revokes** the token at once, and Ledger also revokes every token created through it.\n\n" +
			"Tokens created this way are owned by the root account and bounded by its role. " + apiTokenGrantNote + "\n\n" +
			"Not importable: Ledger never serves a token again, so an imported token would have no secret. Use the `ledger_api_token` data source to read the metadata of tokens that exist.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, MarkdownDescription: "The Ledger identifier of the token (a ULID).",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"name": schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.LengthBetween(1, 100)},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				MarkdownDescription: "A name to recognise the token by, 1 to 100 characters. Changing it replaces the token."},
			"grants": schema.SetAttribute{Optional: true, ElementType: types.StringType,
				Validators:          []validator.Set{setvalidator.SizeAtLeast(1), setvalidator.ValueStringsAre(stringvalidator.LengthAtLeast(1))},
				PlanModifiers:       []planmodifier.Set{setplanmodifier.RequiresReplace()},
				MarkdownDescription: "The grants the token holds (see the `ledger_grants` data source), at least one unless `root` is true. Each must be held by the creating root token; an unknown grant is refused. Requests get only the grants the owner's current role allows. Changing it replaces the token."},
			"root": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false),
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.RequiresReplace()},
				MarkdownDescription: "Create a **root token**: it holds every grant its owner's role holds, now and in later versions, and can itself create tokens. Defaults to `false`. " +
					"Set either `grants` or `root = true`, not both. Prefer narrow grants. Changing it replaces the token."},
			"expires_at": schema.StringAttribute{Optional: true, Validators: []validator.String{rfc3339Validator()},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				MarkdownDescription: "When the token expires (RFC 3339, in the future). Without it the token never expires. Ledger caps it at the expiry of the creating token, silently: see `effective_expires_at` for what applies. " +
					"Changing it replaces the token."},
			"secret": schema.StringAttribute{Computed: true, Sensitive: true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				MarkdownDescription: "The token itself (`ldgr_…`), sent as `Authorization: Bearer <secret>`. **Stored in the Terraform state**: protect the state. Ledger shows it only at creation and cannot return it later."},
			"warning": schema.StringAttribute{Computed: true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				MarkdownDescription: "What a root token can do, as stated by Ledger when it was created; null for other tokens."},
			"owner_id": schema.StringAttribute{Computed: true, MarkdownDescription: "The user that owns the token (the root account).",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"hint":       schema.StringAttribute{Computed: true, MarkdownDescription: "The prefix and the first and last characters of the token, to recognise it (not secret).", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"prefix":     schema.StringAttribute{Computed: true, MarkdownDescription: "`ldgr_` and the first four random characters.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"last_chars": schema.StringAttribute{Computed: true, MarkdownDescription: "The last four characters.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"status":     schema.StringAttribute{Computed: true, MarkdownDescription: "`active`, `expired` or `revoked`, as last read. A token revoked outside Terraform is removed from state on refresh, so the next apply creates a new one."},
			"effective_expires_at": schema.StringAttribute{Computed: true,
				MarkdownDescription: "The expiry Ledger applies (RFC 3339, UTC), or null when the token never expires. It differs from `expires_at` when Ledger capped it."},
			"created_at":      schema.StringAttribute{Computed: true, MarkdownDescription: "When the token was created (RFC 3339, UTC).", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"last_used_at":    schema.StringAttribute{Computed: true, MarkdownDescription: "When it was last used (updated at most once a minute), or null."},
			"revoked_at":      schema.StringAttribute{Computed: true, MarkdownDescription: "When it was revoked, or null."},
			"parent_token_id": schema.StringAttribute{Computed: true, MarkdownDescription: "The token that created this one through the API, or null.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		},
	}
}

func (r *apiTokenResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var root types.Bool
	var grants types.Set
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("root"), &root)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("grants"), &grants)...)
	if resp.Diagnostics.HasError() || root.IsUnknown() || grants.IsUnknown() {
		return
	}
	if root.ValueBool() && !grants.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("grants"), "Grants and root are exclusive", "A root token holds every grant its owner's role holds: remove grants, or set root = false.")
	}
	if !root.ValueBool() && grants.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("grants"), "A token needs grants", "Give at least one grant, or set root = true for a root token.")
	}
}

func (r *apiTokenResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

// apiTokenHint adds advice to the API errors a person meets. It formats only the API error, which never carries the secret.
func apiTokenHint(err error) string {
	switch {
	case client.HasCode(err, client.CodeRootTokenRequired):
		return err.Error() + " Revoking a root token needs the root token of the root account; configure the provider with that token for this change."
	case client.HasCode(err, "forbidden"):
		return err.Error() + " Creating an API token with a token needs the root token of the root account (create it on the Ledger host with `ledger token create --create administrator --root`), " +
			"and every requested grant must be held by it."
	}
	return err.Error()
}

func (r *apiTokenResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan apiTokenModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	n := client.NewAPIToken{Name: plan.Name.ValueString(), Root: plan.Root.ValueBool()}
	if !plan.Grants.IsNull() {
		resp.Diagnostics.Append(plan.Grants.ElementsAs(ctx, &n.Grants, false)...)
	}
	if !plan.ExpiresAt.IsNull() {
		ts, err := time.Parse(time.RFC3339, plan.ExpiresAt.ValueString())
		if err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("expires_at"), "Invalid timestamp", "expires_at must be an RFC 3339 timestamp.")
			return
		}
		n.ExpiresAt = &ts
	}
	if resp.Diagnostics.HasError() {
		return
	}
	created, err := r.client.CreateAPIToken(ctx, n)
	if err != nil {
		resp.Diagnostics.AddError("Creating the API token failed", apiTokenHint(err))
		return
	}
	// The secret exists nowhere else: save it to state before anything else can fail. It is never logged or formatted.
	out := plan
	out.Secret = types.StringValue(created.Secret.Reveal())
	out.Warning = strOrNull(created.Warning)
	resp.Diagnostics.Append(out.applyMetadata(created.Token)...)
	// Keep what was configured where Ledger normalises it: the grant set comes back as sent, expires_at stays as written.
	if !plan.Grants.IsNull() && out.Grants.IsNull() {
		out.Grants = plan.Grants
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, out)...)
}

// Read refreshes the metadata. A token that is gone or revoked leaves the state, so the next apply replaces it.
func (r *apiTokenResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var st apiTokenModel
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	t, err := r.client.GetAPIToken(ctx, st.ID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Reading the API token failed", err.Error())
		return
	}
	if t.Status == client.TokenRevoked {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(st.applyMetadata(t)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, st)...)
}

// Update is never reached with a change: every configurable attribute replaces the token.
func (r *apiTokenResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("An API token cannot be changed", "Every argument of ledger_api_token replaces the token; this is a provider bug.")
}

// Delete revokes the token (and, in Ledger, every token created through it). A token that is already gone is a success.
func (r *apiTokenResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var st apiTokenModel
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.RevokeAPIToken(ctx, st.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Revoking the API token failed", apiTokenHint(err))
	}
}
