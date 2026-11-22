package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// noBankFacts fails when any state attribute holds the fake's balance or account identifier hint.
func noBankFacts(s *terraform.State) error {
	for name, rs := range s.RootModule().Resources {
		for k, v := range rs.Primary.Attributes {
			if strings.Contains(v, "123456.78") || v == "9876" {
				return fmt.Errorf("%s.%s holds a bank fact that must not reach state", name, k)
			}
		}
	}
	return nil
}

const (
	bankConnA = "01JD3Y4C8V9M2Q7W5R6T0XZ001"
	bankConnB = "01JD3Y4C8V9M2Q7W5R6T0XZ002"
)

// fakeBank serves the bank read endpoints. Its connection answer includes the bank facts the provider must never keep
// (a balance and an identifier hint), so a test can prove none of them reaches state. Only GET is served.
func fakeBank(t *testing.T, institutionsStatus int) map[string]func() (tfprotov6.ProviderServer, error) {
	t.Helper()
	requireTerraform(t)
	conn := func(id, status, fresh string) map[string]any {
		return map[string]any{
			"id": id, "provider": "enable_banking", "application": "main", "mode": "app", "environment": "production", "account_access": "all",
			"institution": map[string]any{"id": "BANK_LT", "name": "Example Bank", "country": "LT"},
			"status":      status, "needs_reconnect": status == "reconnect_required", "freshness": fresh,
			"consent_granted_at": "2026-01-01T00:00:00Z", "consent_valid_until": nil, "consent_expires_soon": false,
			"last_error": map[string]any{"kind": "reconnect_required", "code": "SESSION_EXPIRED"},
			"accounts": []map[string]any{{"id": id, "name": "Everyday", "product": "Current", "currency": "EUR", "identifier_hint": "9876",
				"no_longer_reported": false, "transactions_through": "2026-01-31",
				"freshness": map[string]any{"state": fresh, "stale": fresh == "stale", "last_success_at": "2026-01-31T10:00:00Z", "last_attempt_at": nil, "last_error": nil, "notice_code": "transactions_rejected"},
				"balances":  []map[string]any{{"type": "booked", "amount": map[string]any{"amount": "123456.78", "currency": "EUR"}, "reference_date": nil, "obtained_at": "2026-01-31T10:00:00Z"}}}},
			"last_refresh_run": nil, "refresh_running": false, "refresh_available_at": nil, "schedule": nil,
			"as_of": "2026-02-01T00:00:00Z", "version": 3, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-31T10:00:00Z",
		}
	}
	srv := httptest.NewServer(withAPIDocument(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		send := func(code int, v any) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(v)
		}
		if r.Method != http.MethodGet {
			t.Errorf("a bank data source sent %s %s; reads only", r.Method, r.URL.Path)
			send(405, map[string]any{"code": "method_not_allowed", "message": "no", "request_id": "R"})
			return
		}
		p := strings.TrimPrefix(r.URL.Path, "/api/v1")
		switch {
		case p == "/bank-applications":
			send(200, map[string]any{"items": []map[string]any{{"provider": "enable_banking", "name": "main", "mode": "developer", "environment": "sandbox",
				"account_access": "linked_only", "capabilities": map[string]any{"balance_types": []string{"booked", "available"}, "pending_transactions": false,
					"max_consent_validity_days": 0, "max_history_days": 90, "background_fetches_per_day": 4}}}})
		case p == "/bank-applications/main/institutions":
			if institutionsStatus != 200 {
				send(institutionsStatus, map[string]any{"code": "bank_unavailable", "message": "the bank provider is unavailable", "request_id": "R"})
				return
			}
			if r.URL.Query().Get("country") != "LT" {
				t.Errorf("country = %q", r.URL.Query().Get("country"))
			}
			send(200, map[string]any{"items": []map[string]any{{"id": "BANK_LT", "name": "Example Bank", "country": "LT", "max_consent_validity_days": 180}}})
		case p == "/bank-applications/missing/institutions":
			send(404, map[string]any{"code": "not_found", "message": "no application", "request_id": "R"})
		case p == "/bank-connections" && r.URL.Query().Get("cursor") == "":
			send(200, map[string]any{"items": []any{conn(bankConnA, "active", "fresh")}, "next_cursor": "c1"})
		case p == "/bank-connections":
			send(200, map[string]any{"items": []any{conn(bankConnB, "reconnect_required", "stale")}, "next_cursor": nil})
		case p == "/bank-connections/"+bankConnA:
			send(200, conn(bankConnA, "active", "fresh"))
		default:
			send(404, map[string]any{"code": "not_found", "message": "no", "request_id": "R"})
		}
	})))
	t.Cleanup(srv.Close)
	t.Setenv("LEDGER_ENDPOINT", srv.URL)
	t.Setenv("LEDGER_TOKEN", "unit-token")
	return map[string]func() (tfprotov6.ProviderServer, error){"ledger": providerserver.NewProtocol6WithError(New("test")())}
}

func TestBankDataSourcesReadStatusAndFreshness(t *testing.T) {
	factories := fakeBank(t, 200)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{{
			Config: `
data "ledger_bank_applications" "all" {}
data "ledger_bank_institutions" "lt" {
  application = "main"
  country     = "LT"
}
data "ledger_bank_connections" "all" {}
data "ledger_bank_connections" "stale" {
  status = "reconnect_required"
}
data "ledger_bank_connection" "one" {
  id = "` + bankConnA + `"
}
`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.ledger_bank_applications.all", "items.0.mode", "developer"),
				resource.TestCheckResourceAttr("data.ledger_bank_applications.all", "items.0.environment", "sandbox"),
				resource.TestCheckResourceAttr("data.ledger_bank_applications.all", "items.0.account_access", "linked_only"),
				resource.TestCheckResourceAttr("data.ledger_bank_applications.all", "items.0.capabilities.balance_types.1", "available"),
				resource.TestCheckResourceAttr("data.ledger_bank_applications.all", "items.0.capabilities.max_consent_validity_days", "0"),
				resource.TestCheckResourceAttr("data.ledger_bank_institutions.lt", "items.0.max_consent_validity_days", "180"),
				resource.TestCheckResourceAttr("data.ledger_bank_connections.all", "items.#", "2"),
				resource.TestCheckResourceAttr("data.ledger_bank_connections.all", "items.1.freshness", "stale"),
				resource.TestCheckResourceAttr("data.ledger_bank_connections.all", "items.1.accounts.0.freshness.stale", "true"),
				resource.TestCheckResourceAttr("data.ledger_bank_connections.stale", "items.#", "1"),
				resource.TestCheckResourceAttr("data.ledger_bank_connections.stale", "items.0.needs_reconnect", "true"),
				resource.TestCheckResourceAttr("data.ledger_bank_connections.stale", "items.0.last_error.kind", "reconnect_required"),
				resource.TestCheckResourceAttr("data.ledger_bank_connection.one", "institution.name", "Example Bank"),
				resource.TestCheckResourceAttr("data.ledger_bank_connection.one", "accounts.0.currency", "EUR"),
				resource.TestCheckResourceAttr("data.ledger_bank_connection.one", "accounts.0.freshness.notice_code", "transactions_rejected"),
				resource.TestCheckNoResourceAttr("data.ledger_bank_connection.one", "last_refresh_run.id"),
				resource.TestCheckNoResourceAttr("data.ledger_bank_connection.one", "accounts.0.identifier_hint"),
				resource.TestCheckNoResourceAttr("data.ledger_bank_connection.one", "accounts.0.balances.#"),
				// No attribute anywhere holds the fake's balance or identifier hint.
				noBankFacts,
			),
		}},
	})
}

func TestBankInstitutionsProviderFailureKeepsItsCode(t *testing.T) {
	factories := fakeBank(t, 503)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{{
			Config: `
data "ledger_bank_institutions" "lt" {
  application = "main"
  country     = "LT"
}
`,
			ExpectError: regexp.MustCompile(`bank_unavailable`),
		}},
	})
}

func TestBankInstitutionsValidatesCountryAndApplication(t *testing.T) {
	factories := fakeBank(t, 200)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: `
data "ledger_bank_institutions" "x" {
  application = "main"
  country     = "lt"
}
`, ExpectError: regexp.MustCompile(`ISO 3166-1 alpha-2`)},
			{Config: `
data "ledger_bank_institutions" "x" {
  application = "missing"
  country     = "LT"
}
`, ExpectError: regexp.MustCompile(`No such bank application`)},
			{Config: `
data "ledger_bank_connection" "x" {
  id = "01JD3Y4C8V9M2Q7W5R6T0XZ099"
}
`, ExpectError: regexp.MustCompile(`No such bank connection`)},
		},
	})
}
