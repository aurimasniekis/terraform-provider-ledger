package provider

import (
	"context"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aurimasniekis/terraform-provider-ledger/internal/client"
)

var (
	_ datasource.DataSourceWithConfigure = (*bankApplicationsDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*bankInstitutionsDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*bankConnectionsDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*bankConnectionDataSource)(nil)
)

// bankReadNote is shared by every bank data source (ADR-0013).
const bankReadNote = "Read only, and needs an API token with the `connections:read` grant. The values are bank-sourced facts as Ledger reports them: they are never edited here, " +
	"and they are kept apart from your own corrections. Reading changes nothing at the bank, and no consent is started, completed or refreshed (connecting a bank needs you at the bank's own page, in Ledger's UI). " +
	"Account numbers, balances, provider session references and credentials are never read into Terraform state."

// ---- models (the tfsdk tags are the attribute names) ----

type bankCapabilitiesModel struct {
	BalanceTypes           []string `tfsdk:"balance_types"`
	PendingTransactions    bool     `tfsdk:"pending_transactions"`
	MaxConsentValidityDays int64    `tfsdk:"max_consent_validity_days"`
	MaxHistoryDays         int64    `tfsdk:"max_history_days"`
	BackgroundFetchesDay   int64    `tfsdk:"background_fetches_per_day"`
}

type bankApplicationModel struct {
	Provider      string                `tfsdk:"provider_name"`
	Name          string                `tfsdk:"name"`
	Mode          string                `tfsdk:"mode"`
	Environment   string                `tfsdk:"environment"`
	AccountAccess string                `tfsdk:"account_access"`
	Capabilities  bankCapabilitiesModel `tfsdk:"capabilities"`
}

type bankInstitutionModel struct {
	ID                     string `tfsdk:"id"`
	Name                   string `tfsdk:"name"`
	Country                string `tfsdk:"country"`
	MaxConsentValidityDays int64  `tfsdk:"max_consent_validity_days"`
}

type bankErrorModel struct {
	Kind string `tfsdk:"kind"`
	Code string `tfsdk:"code"`
}

type bankFreshnessModel struct {
	State         string          `tfsdk:"state"`
	Stale         bool            `tfsdk:"stale"`
	LastSuccessAt *string         `tfsdk:"last_success_at"`
	LastAttemptAt *string         `tfsdk:"last_attempt_at"`
	LastError     *bankErrorModel `tfsdk:"last_error"`
	NoticeCode    *string         `tfsdk:"notice_code"`
}

type bankAccountModel struct {
	ID                  string             `tfsdk:"id"`
	Name                string             `tfsdk:"name"`
	Product             string             `tfsdk:"product"`
	Currency            string             `tfsdk:"currency"`
	NoLongerReported    bool               `tfsdk:"no_longer_reported"`
	TransactionsThrough *string            `tfsdk:"transactions_through"`
	Freshness           bankFreshnessModel `tfsdk:"freshness"`
}

type bankRefreshRunModel struct {
	ID                   string          `tfsdk:"id"`
	StartedAt            string          `tfsdk:"started_at"`
	FinishedAt           *string         `tfsdk:"finished_at"`
	Outcome              string          `tfsdk:"outcome"`
	Error                *bankErrorModel `tfsdk:"error"`
	AccountsFailed       int64           `tfsdk:"accounts_failed"`
	TransactionsInserted int64           `tfsdk:"transactions_inserted"`
	TransactionsRejected int64           `tfsdk:"transactions_rejected"`
}

type bankScheduleModel struct {
	State               string  `tfsdk:"state"`
	NextAttemptAt       *string `tfsdk:"next_attempt_at"`
	ConsecutiveFailures int64   `tfsdk:"consecutive_failures"`
}

type bankInstitutionRefModel struct {
	ID      string `tfsdk:"id"`
	Name    string `tfsdk:"name"`
	Country string `tfsdk:"country"`
}

type bankConnectionModel struct {
	ID                 string                  `tfsdk:"id"`
	Provider           string                  `tfsdk:"provider_name"`
	Application        string                  `tfsdk:"application"`
	Mode               string                  `tfsdk:"mode"`
	Environment        string                  `tfsdk:"environment"`
	AccountAccess      string                  `tfsdk:"account_access"`
	Institution        bankInstitutionRefModel `tfsdk:"institution"`
	Status             string                  `tfsdk:"status"`
	NeedsReconnect     bool                    `tfsdk:"needs_reconnect"`
	Freshness          string                  `tfsdk:"freshness"`
	ConsentGrantedAt   *string                 `tfsdk:"consent_granted_at"`
	ConsentValidUntil  *string                 `tfsdk:"consent_valid_until"`
	ConsentExpiresSoon bool                    `tfsdk:"consent_expires_soon"`
	LastError          *bankErrorModel         `tfsdk:"last_error"`
	Accounts           []bankAccountModel      `tfsdk:"accounts"`
	LastRefreshRun     *bankRefreshRunModel    `tfsdk:"last_refresh_run"`
	RefreshRunning     bool                    `tfsdk:"refresh_running"`
	RefreshAvailableAt *string                 `tfsdk:"refresh_available_at"`
	Schedule           *bankScheduleModel      `tfsdk:"schedule"`
	AsOf               string                  `tfsdk:"as_of"`
	Version            int64                   `tfsdk:"version"`
	CreatedAt          string                  `tfsdk:"created_at"`
	UpdatedAt          string                  `tfsdk:"updated_at"`
}

func bankErrorFrom(e *client.BankError) *bankErrorModel {
	if e == nil {
		return nil
	}
	return &bankErrorModel{Kind: e.Kind, Code: e.Code}
}

func bankConnectionFrom(c client.BankConnection) bankConnectionModel {
	m := bankConnectionModel{
		ID: c.ID, Provider: c.Provider, Application: c.Application, Mode: c.Mode, Environment: c.Environment, AccountAccess: c.AccountAccess,
		Institution: bankInstitutionRefModel{ID: c.Institution.ID, Name: c.Institution.Name, Country: c.Institution.Country},
		Status:      c.Status, NeedsReconnect: c.NeedsReconnect, Freshness: c.Freshness, ConsentGrantedAt: c.ConsentGrantedAt,
		ConsentValidUntil: c.ConsentValidUntil, ConsentExpiresSoon: c.ConsentExpiresSoon, LastError: bankErrorFrom(c.LastError),
		Accounts: make([]bankAccountModel, 0, len(c.Accounts)), RefreshRunning: c.RefreshRunning, RefreshAvailableAt: c.RefreshAvailableAt,
		AsOf: c.AsOf, Version: c.Version, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
	for _, a := range c.Accounts {
		m.Accounts = append(m.Accounts, bankAccountModel{ID: a.ID, Name: a.Name, Product: a.Product, Currency: a.Currency,
			NoLongerReported: a.NoLongerReported, TransactionsThrough: a.TransactionsThrough,
			Freshness: bankFreshnessModel{State: a.Freshness.State, Stale: a.Freshness.Stale, LastSuccessAt: a.Freshness.LastSuccessAt,
				LastAttemptAt: a.Freshness.LastAttemptAt, LastError: bankErrorFrom(a.Freshness.LastError), NoticeCode: a.Freshness.NoticeCode}})
	}
	if r := c.LastRefreshRun; r != nil {
		m.LastRefreshRun = &bankRefreshRunModel{ID: r.ID, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt, Outcome: r.Outcome, Error: bankErrorFrom(r.Error),
			AccountsFailed: r.AccountsFailed, TransactionsInserted: r.TransactionsInserted, TransactionsRejected: r.TransactionsRejected}
	}
	if s := c.Schedule; s != nil {
		m.Schedule = &bankScheduleModel{State: s.State, NextAttemptAt: s.NextAttemptAt, ConsecutiveFailures: s.ConsecutiveFailures}
	}
	return m
}

// ---- schemas ----

func str(desc string) schema.StringAttribute {
	return schema.StringAttribute{Computed: true, MarkdownDescription: desc}
}
func boolean(desc string) schema.BoolAttribute {
	return schema.BoolAttribute{Computed: true, MarkdownDescription: desc}
}
func num(desc string) schema.Int64Attribute {
	return schema.Int64Attribute{Computed: true, MarkdownDescription: desc}
}

func bankErrorSchema(desc string) schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{Computed: true, MarkdownDescription: desc + " Null when there was none. It carries the failure kind and the provider's code, never a bank payload.",
		Attributes: map[string]schema.Attribute{
			"kind": str("`reconnect_required`, `rate_limited`, `retryable`, `permanent` or `canceled`. Only `reconnect_required` needs a new consent; `rate_limited` and `retryable` can be retried later."),
			"code": str("The provider's error code; may be empty."),
		}}
}

func bankApplicationItemSchema() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"provider_name":  str("The banking-data provider, for example `enable_banking`."),
		"name":           str("The operator's name for the application; connections record it."),
		"mode":           str("The provider's own mode terminology, `app` or `developer`. It is neither production nor sandbox."),
		"environment":    str("`production` or `sandbox`."),
		"account_access": str("`all` reaches every account the person consents to; `linked_only` (developer mode) only accounts linked to the application at the provider."),
		"capabilities": schema.SingleNestedAttribute{Computed: true, MarkdownDescription: "What the application can do. A number of zero means not known, never none.",
			Attributes: map[string]schema.Attribute{
				"balance_types":              schema.ListAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "The balance types the provider can report: `booked`, `available`, `expected` or `other`."},
				"pending_transactions":       boolean("Whether pending transactions are available."),
				"max_consent_validity_days":  num("The longest consent validity in days, or 0 when not known."),
				"max_history_days":           num("How many days of history can be fetched, or 0 when not known."),
				"background_fetches_per_day": num("How many background fetches per day the provider allows, or 0 when not known."),
			}},
	}
}

func bankFreshnessSchema() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{Computed: true, MarkdownDescription: "How current the account's data is.",
		Attributes: map[string]schema.Attribute{
			"state":           str("`fresh`, `stale`, `failed`, `reconnect_required`, `disconnected` or `never_refreshed`."),
			"stale":           boolean("Whether the data is older than Ledger's freshness threshold."),
			"last_success_at": str("When the account last refreshed successfully (RFC 3339, UTC), or null."),
			"last_attempt_at": str("When a refresh was last tried (RFC 3339, UTC), or null."),
			"last_error":      bankErrorSchema("Why the last attempt failed."),
			"notice_code":     str("A note about the last refresh, such as `transactions_rejected`, or null."),
		}}
}

func bankConnectionItemSchema() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"id":             str("The Ledger identifier of the connection."),
		"provider_name":  str("The banking-data provider."),
		"application":    str("The name of the provider application the connection was made through."),
		"mode":           str("The provider's own mode terminology, `app` or `developer` (neither is production or sandbox)."),
		"environment":    str("`production` or `sandbox`."),
		"account_access": str("`all` or `linked_only`."),
		"institution": schema.SingleNestedAttribute{Computed: true, MarkdownDescription: "The bank.", Attributes: map[string]schema.Attribute{
			"id": str("The provider's identifier of the bank."), "name": str("The bank's name."), "country": str("ISO 3166-1 alpha-2 country code."),
		}},
		"status":               str("`pending`, `active`, `reconnect_required`, `failed` or `disconnected`."),
		"needs_reconnect":      boolean("True only when a new consent is the way forward (status `reconnect_required`). Never true for a retryable failure."),
		"freshness":            str("The worst freshness among the accounts, or the connection's own state when it has none: `fresh`, `stale`, `failed`, `reconnect_required`, `disconnected` or `never_refreshed`. Stale or failed data is shown as such, never as current."),
		"consent_granted_at":   str("When consent was granted (RFC 3339, UTC), or null."),
		"consent_valid_until":  str("When the consent ends (RFC 3339, UTC), or null."),
		"consent_expires_soon": boolean("An active consent ends within 14 days."),
		"last_error":           bankErrorSchema("Why the connection last failed."),
		"accounts": schema.ListNestedAttribute{Computed: true, MarkdownDescription: "The accounts of the connection with their freshness. Balances, account numbers and identifier hints are deliberately not included.",
			NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"id":                   str("The Ledger identifier of the account."),
				"name":                 str("The account's name as the bank reports it."),
				"product":              str("The bank's product name."),
				"currency":             str("ISO 4217 currency, or `XXX` when the bank reports the account in several currencies."),
				"no_longer_reported":   boolean("The bank no longer reports the account."),
				"transactions_through": str("The date (YYYY-MM-DD) transactions are held up to, or null."),
				"freshness":            bankFreshnessSchema(),
			}}},
		"last_refresh_run": schema.SingleNestedAttribute{Computed: true, MarkdownDescription: "The latest refresh run, or null when there was none.", Attributes: map[string]schema.Attribute{
			"id":                    str("The run identifier."),
			"started_at":            str("When it started (RFC 3339, UTC)."),
			"finished_at":           str("When it finished (RFC 3339, UTC), or null while running."),
			"outcome":               str("`running`, `succeeded`, `partial`, `failed` or `interrupted`."),
			"error":                 bankErrorSchema("Why the run failed."),
			"accounts_failed":       num("How many accounts failed."),
			"transactions_inserted": num("How many transactions the run added."),
			"transactions_rejected": num("How many transactions Ledger rejected."),
		}},
		"refresh_running":      boolean("True while a refresh is going on in the background."),
		"refresh_available_at": str("While in the future, a refresh on request is refused until this time (RFC 3339, UTC). Null when one is allowed now."),
		"schedule": schema.SingleNestedAttribute{Computed: true, MarkdownDescription: "The background scheduler's view; null when background refresh is off or has not seen the connection yet.", Attributes: map[string]schema.Attribute{
			"state":                str("`waiting`, `due`, `running`, `backoff`, `rate_limited`, `budget_exhausted`, `not_active` or `no_provider`."),
			"next_attempt_at":      str("The next attempt (RFC 3339, UTC), or null."),
			"consecutive_failures": num("How many refreshes in a row failed."),
		}},
		"as_of":      str("When Ledger computed the freshness (RFC 3339, UTC). Every plan reads it again, so a value that changes between plans is not drift in your configuration."),
		"version":    num("The object's version."),
		"created_at": str("When the connection was recorded (RFC 3339, UTC)."),
		"updated_at": str("When it last changed (RFC 3339, UTC)."),
	}
}

// ---- ledger_bank_applications ----

type bankApplicationsDataSource struct{ client *client.Client }

// NewBankApplicationsDataSource is the data source constructor.
func NewBankApplicationsDataSource() datasource.DataSource { return &bankApplicationsDataSource{} }

func (d *bankApplicationsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bank_applications"
}

func (d *bankApplicationsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the banking-data provider applications Ledger is configured with, with each one's mode, environment, account access and capabilities. " +
			"Credentials are never shown, and Terraform cannot configure an application. " + bankReadNote,
		Attributes: map[string]schema.Attribute{
			"items": schema.ListNestedAttribute{Computed: true, MarkdownDescription: "The applications.",
				NestedObject: schema.NestedAttributeObject{Attributes: bankApplicationItemSchema()}},
		},
	}
}

func (d *bankApplicationsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *bankApplicationsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	apps, err := d.client.ListBankApplications(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Listing the bank applications failed", err.Error())
		return
	}
	items := make([]bankApplicationModel, 0, len(apps))
	for _, a := range apps {
		bt := a.Capabilities.BalanceTypes
		if bt == nil {
			bt = []string{}
		}
		items = append(items, bankApplicationModel{Provider: a.Provider, Name: a.Name, Mode: a.Mode, Environment: a.Environment, AccountAccess: a.AccountAccess,
			Capabilities: bankCapabilitiesModel{BalanceTypes: bt, PendingTransactions: a.Capabilities.PendingTransactions,
				MaxConsentValidityDays: a.Capabilities.MaxConsentValidityDays, MaxHistoryDays: a.Capabilities.MaxHistoryDays,
				BackgroundFetchesDay: a.Capabilities.BackgroundFetchesDay}})
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("items"), items)...)
}

// ---- ledger_bank_institutions ----

type bankInstitutionsDataSource struct{ client *client.Client }

// NewBankInstitutionsDataSource is the data source constructor.
func NewBankInstitutionsDataSource() datasource.DataSource { return &bankInstitutionsDataSource{} }

func (d *bankInstitutionsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bank_institutions"
}

var countryRegexp = regexp.MustCompile("^[A-Z]{2}$")

func (d *bankInstitutionsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the banks a provider application offers in a country. Ledger asks the provider each time, so the read needs the provider to be reachable: " +
			"a provider outage or rate limit (`bank_unavailable`, `bank_rate_limited`) fails the read with that code, and it can be retried later. " + bankReadNote,
		Attributes: map[string]schema.Attribute{
			"application": schema.StringAttribute{Required: true, MarkdownDescription: "The name of the application, as `ledger_bank_applications` lists it.",
				Validators: []validator.String{stringvalidator.LengthAtLeast(1)}},
			"country": schema.StringAttribute{Required: true, MarkdownDescription: "ISO 3166-1 alpha-2 country code in capitals, for example `LT`.",
				Validators: []validator.String{stringvalidator.RegexMatches(countryRegexp, "must be an ISO 3166-1 alpha-2 country code in capitals")}},
			"items": schema.ListNestedAttribute{Computed: true, MarkdownDescription: "The institutions.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"id":                        str("The provider's identifier of the bank."),
					"name":                      str("The bank's name."),
					"country":                   str("The country code."),
					"max_consent_validity_days": num("The longest consent validity in days the bank allows, or 0 when not known."),
				}}},
		},
	}
}

func (d *bankInstitutionsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *bankInstitutionsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var app, country types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("application"), &app)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("country"), &country)...)
	if resp.Diagnostics.HasError() {
		return
	}
	list, err := d.client.ListBankInstitutions(ctx, app.ValueString(), country.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.Diagnostics.AddAttributeError(path.Root("application"), "No such bank application", "Ledger has no provider application with that name; see the ledger_bank_applications data source.")
			return
		}
		resp.Diagnostics.AddError("Listing the bank institutions failed", err.Error())
		return
	}
	items := make([]bankInstitutionModel, 0, len(list))
	for _, i := range list {
		items = append(items, bankInstitutionModel{ID: i.ID, Name: i.Name, Country: i.Country, MaxConsentValidityDays: i.MaxConsentValidityDays})
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("application"), app)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("country"), country)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("items"), items)...)
}

// ---- ledger_bank_connections ----

type bankConnectionsDataSource struct{ client *client.Client }

// NewBankConnectionsDataSource is the data source constructor.
func NewBankConnectionsDataSource() datasource.DataSource { return &bankConnectionsDataSource{} }

func (d *bankConnectionsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bank_connections"
}

func (d *bankConnectionsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists Ledger's bank connections, oldest first, with their status, consent window and the freshness of each account. " + bankReadNote +
			" The `as_of` and freshness values change as time passes, so a configuration that depends on them is re-evaluated on every plan.",
		Attributes: map[string]schema.Attribute{
			"status": schema.StringAttribute{Optional: true, MarkdownDescription: "Only list connections with this status.",
				Validators: []validator.String{stringvalidator.OneOf("pending", "active", "reconnect_required", "failed", "disconnected")}},
			"items": schema.ListNestedAttribute{Computed: true, MarkdownDescription: "The connections.",
				NestedObject: schema.NestedAttributeObject{Attributes: bankConnectionItemSchema()}},
		},
	}
}

func (d *bankConnectionsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *bankConnectionsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var status types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("status"), &status)...)
	if resp.Diagnostics.HasError() {
		return
	}
	list, err := d.client.ListBankConnections(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Listing the bank connections failed", err.Error())
		return
	}
	items := make([]bankConnectionModel, 0, len(list))
	for _, c := range list {
		if !status.IsNull() && c.Status != status.ValueString() {
			continue
		}
		items = append(items, bankConnectionFrom(c))
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("status"), status)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("items"), items)...)
}

// ---- ledger_bank_connection ----

type bankConnectionDataSource struct{ client *client.Client }

// NewBankConnectionDataSource is the data source constructor.
func NewBankConnectionDataSource() datasource.DataSource { return &bankConnectionDataSource{} }

func (d *bankConnectionDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bank_connection"
}

func (d *bankConnectionDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := bankConnectionItemSchema()
	attrs["id"] = schema.StringAttribute{Required: true, MarkdownDescription: "The Ledger identifier of the connection.", Validators: []validator.String{idValidator()}}
	resp.Schema = schema.Schema{MarkdownDescription: "Reads one Ledger bank connection by identifier: its status, consent window and the freshness of each account. " + bankReadNote, Attributes: attrs}
}

func (d *bankConnectionDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *bankConnectionDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var id types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("id"), &id)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c, err := d.client.GetBankConnection(ctx, id.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.Diagnostics.AddAttributeError(path.Root("id"), "No such bank connection", "Ledger has no bank connection with that identifier.")
			return
		}
		resp.Diagnostics.AddError("Reading the bank connection failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, bankConnectionFrom(c))...)
}
