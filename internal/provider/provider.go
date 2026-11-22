// Package provider implements the Ledger Terraform provider with the Terraform Plugin Framework.
//
// Each configurable object (decision D18) is added to Resources and DataSources by the Terraform resource tasks, and
// receives the *client.Client built in Configure through its Configure method. The organisation objects (categories,
// labels, buckets and their assignments, task T3) are registered; the remaining objects are task T4.
package provider

import (
	"context"
	"errors"
	"os"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aurimasniekis/terraform-provider-ledger/internal/client"
)

var _ provider.Provider = (*ledgerProvider)(nil)

type ledgerProvider struct {
	version  string
	getenv   func(string) string
	readFile func(string) ([]byte, error)
	// checkAPIVersion is the API version check (ADR-0008). Nil means the real check; tests replace it.
	checkAPIVersion func(context.Context, *client.Client) (client.APIVersionCheck, error)
}

type providerModel struct {
	Endpoint  types.String `tfsdk:"endpoint"`
	Token     types.String `tfsdk:"token"`
	TokenFile types.String `tfsdk:"token_file"`
}

// New returns a provider factory for the given provider version.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &ledgerProvider{version: version, getenv: os.Getenv, readFile: os.ReadFile}
	}
}

func (p *ledgerProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "ledger"
	resp.Version = p.version
}

func (p *ledgerProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manage a [Ledger](https://github.com/aurimasniekis/go-ledger) server through its Management API.\n\n" +
			"Each setting can also come from the environment. **Precedence** (first one set wins, and an unusable source " +
			"is an error rather than a fall-through): `endpoint` attribute, then `" + EnvEndpoint + "`; " +
			"`token` attribute, then `token_file` attribute, then `" + EnvToken + "`, then `" + EnvTokenFile + "`. " +
			"Blank values count as unset. On configuration the provider reads the API version the server serves and refuses a " +
			"server on an unsupported major version.",
		Attributes: map[string]schema.Attribute{
			"endpoint": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Base URL of the Ledger server, for example `https://ledger.example.com`. " +
					"Falls back to the `" + EnvEndpoint + "` environment variable. Must be an http or https URL " +
					"without credentials, query or fragment. Use https for any non-local server.",
			},
			"token": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				MarkdownDescription: "Ledger API token. Falls back to `token_file`, then the `" + EnvToken + "` environment " +
					"variable, then `" + EnvTokenFile + "`. Prefer the environment or `token_file` so the token is not " +
					"written into configuration files. Conflicts with `token_file`.",
				Validators: []validator.String{stringvalidator.ConflictsWith(path.MatchRoot("token_file"))},
			},
			"token_file": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Path to a file that contains only the API token (one line; surrounding whitespace is " +
					"ignored). Falls back to the `" + EnvTokenFile + "` environment variable when neither `token` nor " +
					"`token_file` is set and `" + EnvToken + "` is empty. Conflicts with `token`.",
				Validators: []validator.String{stringvalidator.ConflictsWith(path.MatchRoot("token"))},
			},
		},
	}
}

func (p *ledgerProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var model providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// A value that is not known yet (for example computed by another resource) cannot configure the client.
	for _, f := range []struct {
		name string
		val  types.String
	}{{"endpoint", model.Endpoint}, {"token", model.Token}, {"token_file", model.TokenFile}} {
		if f.val.IsUnknown() {
			resp.Diagnostics.AddAttributeError(path.Root(f.name), "Unknown Ledger provider value",
				"The provider cannot be configured while "+f.name+" is unknown. Use a value known at plan time, or set the "+
					"matching environment variable.")
		}
	}
	if resp.Diagnostics.HasError() {
		return
	}

	getenv, readFile := p.getenv, p.readFile
	if getenv == nil {
		getenv = os.Getenv
	}
	if readFile == nil {
		readFile = os.ReadFile
	}
	cfg, cerr := resolve(rawConfig{
		Endpoint:  model.Endpoint.ValueString(),
		Token:     model.Token.ValueString(),
		TokenFile: model.TokenFile.ValueString(),
	}, getenv, readFile)
	if cerr != nil {
		if cerr.Attribute == "" {
			resp.Diagnostics.AddError(cerr.Summary, cerr.Detail)
		} else {
			resp.Diagnostics.AddAttributeError(path.Root(cerr.Attribute), cerr.Summary, cerr.Detail)
		}
		return
	}

	c, err := client.New(client.Config{Endpoint: cfg.Endpoint, Token: client.Secret(cfg.Token)})
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("endpoint"), "Invalid Ledger provider configuration",
			err.Error()+" (endpoint taken from the "+cfg.EndpointSource+").")
		return
	}
	if client.IsPlainHTTPRemote(c.BaseURL()) {
		resp.Diagnostics.AddAttributeWarning(path.Root("endpoint"), "Ledger endpoint is not encrypted",
			"The API token will be sent over plain http to a non-local host. Use an https endpoint.")
	}

	check := p.checkAPIVersion
	if check == nil {
		check = func(ctx context.Context, c *client.Client) (client.APIVersionCheck, error) {
			return c.CheckAPIVersion(ctx)
		}
	}
	v, err := check(ctx, c)
	if err != nil {
		summary := "Cannot verify the Ledger API version"
		var mismatch *client.APIMajorError
		if errors.As(err, &mismatch) {
			summary = "Unsupported Ledger API version"
		}
		resp.Diagnostics.AddAttributeError(path.Root("endpoint"), summary, err.Error())
		return
	}
	if v.Warning != "" {
		resp.Diagnostics.AddAttributeWarning(path.Root("endpoint"), "Newer Ledger API version", v.Warning)
	}

	resp.DataSourceData = c
	resp.ResourceData = c
}

func (p *ledgerProvider) Resources(context.Context) []func() resource.Resource {
	// Each remaining configurable object (decision D18) adds its constructors here.
	return []func() resource.Resource{
		// Organisation (task T3, decision D12, go-ledger ADR-0053).
		NewCategoryResource, NewLabelResource, NewBucketResource,
		NewCategoryAssignmentResource, NewLabelAssignmentResource, NewBucketMemberResource,
		NewMerchantResource, NewMerchantAliasResource,
		// Commitments (task T4, decision D6, D9, D14).
		NewCommitmentResource,
		// Deferral plans (task F3, go-ledger ADR-0050, ADR-0010 of this provider).
		NewDeferralPlanResource, NewDeferralAdjustmentResource,
	}
}

func (p *ledgerProvider) DataSources(context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewCategoryDataSource, NewLabelDataSource, NewBucketDataSource,
		NewCategoriesDataSource, NewLabelsDataSource, NewBucketsDataSource,
		NewObjectOrganisationDataSource, NewBucketMembersDataSource,
		NewMerchantDataSource, NewMerchantsDataSource,
		NewCommitmentDataSource, NewCommitmentsDataSource,
		NewDeferralPlanDataSource, NewDeferralPlansDataSource, NewDeferralHistoryDataSource, NewOccurrenceDataSource,
		// Read-only bank data sources (task F6, ADR-0012).
		NewBankApplicationsDataSource, NewBankInstitutionsDataSource, NewBankConnectionsDataSource, NewBankConnectionDataSource,
	}
}
