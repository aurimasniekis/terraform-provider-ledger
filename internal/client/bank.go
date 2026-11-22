package client

import (
	"context"
	"net/http"
	"net/url"
)

// BankCapabilities is what a provider application can do. Zero means not known, never none.
type BankCapabilities struct {
	BalanceTypes           []string `json:"balance_types"`
	PendingTransactions    bool     `json:"pending_transactions"`
	MaxConsentValidityDays int64    `json:"max_consent_validity_days"`
	MaxHistoryDays         int64    `json:"max_history_days"`
	BackgroundFetchesDay   int64    `json:"background_fetches_per_day"`
}

// BankApplication is a banking-data provider application the deployment is configured with. Credentials are never part of it.
type BankApplication struct {
	Provider      string           `json:"provider"`
	Name          string           `json:"name"`
	Mode          string           `json:"mode"`
	Environment   string           `json:"environment"`
	AccountAccess string           `json:"account_access"`
	Capabilities  BankCapabilities `json:"capabilities"`
}

// BankInstitution is a bank an application offers in a country.
type BankInstitution struct {
	ID                     string `json:"id"`
	Name                   string `json:"name"`
	Country                string `json:"country"`
	MaxConsentValidityDays int64  `json:"max_consent_validity_days"`
}

// BankError says why a refresh or an account's last attempt failed. It carries no bank payload.
type BankError struct {
	Kind string `json:"kind"`
	Code string `json:"code"`
}

// BankFreshness is how current an account's data is.
type BankFreshness struct {
	State         string     `json:"state"`
	Stale         bool       `json:"stale"`
	LastSuccessAt *string    `json:"last_success_at"`
	LastAttemptAt *string    `json:"last_attempt_at"`
	LastError     *BankError `json:"last_error"`
	NoticeCode    *string    `json:"notice_code"`
}

// BankAccount is an account of a connection. The answer also carries balances and an identifier hint; they are bank
// facts the provider deliberately does not decode, so they can never reach state (ADR-0012).
type BankAccount struct {
	ID                  string        `json:"id"`
	Name                string        `json:"name"`
	Product             string        `json:"product"`
	Currency            string        `json:"currency"`
	NoLongerReported    bool          `json:"no_longer_reported"`
	TransactionsThrough *string       `json:"transactions_through"`
	Freshness           BankFreshness `json:"freshness"`
}

// BankRefreshRun is the latest refresh of a connection.
type BankRefreshRun struct {
	ID                   string     `json:"id"`
	StartedAt            string     `json:"started_at"`
	FinishedAt           *string    `json:"finished_at"`
	Outcome              string     `json:"outcome"`
	Error                *BankError `json:"error"`
	AccountsFailed       int64      `json:"accounts_failed"`
	TransactionsInserted int64      `json:"transactions_inserted"`
	TransactionsRejected int64      `json:"transactions_rejected"`
}

// BankSchedule is the background scheduler's view of a connection.
type BankSchedule struct {
	State               string  `json:"state"`
	NextAttemptAt       *string `json:"next_attempt_at"`
	ConsecutiveFailures int64   `json:"consecutive_failures"`
}

// BankConnection is a connection to a bank, as Ledger reports it: status and freshness. Provider session references,
// authorisation codes and account numbers are never returned by the API, and balances are not decoded here.
type BankConnection struct {
	ID            string `json:"id"`
	Provider      string `json:"provider"`
	Application   string `json:"application"`
	Mode          string `json:"mode"`
	Environment   string `json:"environment"`
	AccountAccess string `json:"account_access"`
	Institution   struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Country string `json:"country"`
	} `json:"institution"`
	Status             string          `json:"status"`
	NeedsReconnect     bool            `json:"needs_reconnect"`
	Freshness          string          `json:"freshness"`
	ConsentGrantedAt   *string         `json:"consent_granted_at"`
	ConsentValidUntil  *string         `json:"consent_valid_until"`
	ConsentExpiresSoon bool            `json:"consent_expires_soon"`
	LastError          *BankError      `json:"last_error"`
	Accounts           []BankAccount   `json:"accounts"`
	LastRefreshRun     *BankRefreshRun `json:"last_refresh_run"`
	RefreshRunning     bool            `json:"refresh_running"`
	RefreshAvailableAt *string         `json:"refresh_available_at"`
	Schedule           *BankSchedule   `json:"schedule"`
	AsOf               string          `json:"as_of"`
	Version            int64           `json:"version"`
	CreatedAt          string          `json:"created_at"`
	UpdatedAt          string          `json:"updated_at"`
}

type bankConnectionPage struct {
	Items      []BankConnection `json:"items"`
	NextCursor *string          `json:"next_cursor"`
}

// ListBankApplications reads the configured provider applications.
func (c *Client) ListBankApplications(ctx context.Context) ([]BankApplication, error) {
	var out struct {
		Items []BankApplication `json:"items"`
	}
	err := c.do(ctx, request{Method: http.MethodGet, Path: "/bank-applications", Out: &out})
	return out.Items, err
}

// ListBankInstitutions asks the provider which banks an application offers in a country (ISO 3166-1 alpha-2, capitals).
// It is a read; a provider failure comes back as an API error with a bank_* code.
func (c *Client) ListBankInstitutions(ctx context.Context, application, country string) ([]BankInstitution, error) {
	var out struct {
		Items []BankInstitution `json:"items"`
	}
	err := c.do(ctx, request{Method: http.MethodGet, Path: "/bank-applications/" + url.PathEscape(application) + "/institutions",
		Query: url.Values{"country": {country}}, Out: &out})
	return out.Items, err
}

// GetBankConnection reads one connection.
func (c *Client) GetBankConnection(ctx context.Context, id string) (BankConnection, error) {
	if err := checkID("id", id); err != nil {
		return BankConnection{}, err
	}
	var out BankConnection
	err := c.do(ctx, request{Method: http.MethodGet, Path: "/bank-connections/" + id, Out: &out})
	return out, err
}

// ListBankConnections reads every page of connections, oldest first.
func (c *Client) ListBankConnections(ctx context.Context) ([]BankConnection, error) {
	q := url.Values{"limit": {"200"}}
	var all []BankConnection
	for {
		var page bankConnectionPage
		if err := c.do(ctx, request{Method: http.MethodGet, Path: "/bank-connections", Query: q, Out: &page}); err != nil {
			return nil, err
		}
		all = append(all, page.Items...)
		if page.NextCursor == nil || *page.NextCursor == "" || len(page.Items) == 0 {
			return all, nil
		}
		q.Set("cursor", *page.NextCursor)
	}
}
