package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestReportSchemasAreValid(t *testing.T) {
	seen := map[string]bool{}
	for _, mk := range reportDataSources() {
		ds := mk()
		var meta datasource.MetadataResponse
		ds.Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "ledger"}, &meta)
		if seen[meta.TypeName] {
			t.Errorf("%s is registered twice", meta.TypeName)
		}
		seen[meta.TypeName] = true
		var resp datasource.SchemaResponse
		ds.Schema(context.Background(), datasource.SchemaRequest{}, &resp)
		if d := resp.Schema.ValidateImplementation(context.Background()); d.HasError() {
			t.Errorf("%s: %v", meta.TypeName, d)
		}
	}
	for _, want := range []string{"ledger_me", "ledger_grants", "ledger_auth_methods", "ledger_exchange_rates", "ledger_rates_status",
		"ledger_occurrences", "ledger_monthly_summary", "ledger_outlook", "ledger_payment_matches", "ledger_payment_match",
		"ledger_audit_entries", "ledger_merchant_sources"} {
		if !seen[want] {
			t.Errorf("%s is missing", want)
		}
	}
}

func TestNodeValueKeepsExactAmountsAndNulls(t *testing.T) {
	n := money("")
	v, d := n.value(map[string]any{"amount": "0.10", "currency": "EUR"})
	if d.HasError() || !strings.Contains(v.String(), `"0.10"`) {
		t.Fatalf("%v %v", v, d)
	}
	if v, d := n.value(nil); d.HasError() || !v.IsNull() {
		t.Fatalf("nil must be null: %v %v", v, d)
	}
	if _, d := n.value("12.50"); !d.HasError() {
		t.Fatal("a string where an object is expected must be reported")
	}
	if _, d := integer("").value(json.Number("1.5")); !d.HasError() {
		t.Fatal("a fraction is not an integer")
	}
}

// fakeReports serves canned reports and records every query it saw.
type fakeReports struct {
	mu      sync.Mutex
	queries []string
}

const (
	occID   = "01JD3Y4C8V9M2Q7W5R6T0XZ001"
	matchID = "01JD3Y4C8V9M2Q7W5R6T0XZ002"
)

func (f *fakeReports) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.queries = append(f.queries, r.URL.Path+"?"+r.URL.RawQuery)
	f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer unit-token" {
		http.Error(w, "no", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	occ := func(id string) string {
		return `{"id":"` + id + `","commitment_id":"01JD3Y4C8V9M2Q7W5R6T0XZ00C","month":"2026-03","due_date":"2026-03-05","amount":{"amount":"850.10","currency":"EUR"},"version":2,"created_at":"2026-03-01T00:00:00Z","updated_at":"2026-03-01T00:00:00Z","cancelled_at":null,"cancelled_reason":null,
"payment":{"status":"paid","derived_status":"overdue","timing":"overdue","paid_amount":{"amount":"0.00","currency":"EUR"},"outstanding_amount":{"amount":"850.10","currency":"EUR"},"overpaid_amount":{"amount":"0.00","currency":"EUR"},"shortfall_amount":{"amount":"0.00","currency":"EUR"},"unconverted_currencies":["USD"],"evidence_count":0,"manual_evidence_count":0,"bank_evidence_count":0,"grace_until":null,"within_grace":false,"leeway":null,
"override":{"status":"paid","reason":"paid in cash","set_at":"2026-03-06T00:00:00Z","applied":true,"previous_status":null}}}`
	}
	switch r.URL.Path {
	case "/api/v1/me":
		_, _ = w.Write([]byte(`{"kind":"token","user_id":"01JD3Y4C8V9M2Q7W5R6T0XZ00U","token_id":"01JD3Y4C8V9M2Q7W5R6T0XZ00T","role":"administrator","grants":["commitments:read","audit:read"]}`))
	case "/api/v1/occurrences":
		if r.URL.Query().Get("cursor") == "" {
			_, _ = w.Write([]byte(`{"items":[` + occ(occID) + `],"next_cursor":"next"}`))
		} else {
			_, _ = w.Write([]byte(`{"items":[` + occ("01JD3Y4C8V9M2Q7W5R6T0XZ003") + `],"next_cursor":null}`))
		}
	case "/api/v1/audit":
		_, _ = w.Write([]byte(`{"items":[{"id":"01JD3Y4C8V9M2Q7W5R6T0XZ010","action":"commitment.create","actor_kind":"token","actor_user_id":null,"actor_token_id":"01JD3Y4C8V9M2Q7W5R6T0XZ00T","target_type":"commitment","target_id":"x","at":"2026-03-01T00:00:00Z","details":{"n":12345678901234567890}}],"next_cursor":null}`))
	case "/api/v1/outlook":
		_, _ = w.Write([]byte(`{"as_of":"2026-03-10","notice":"Not a guarantee.","slice":null,"window":{"not_recorded_count":0,"from":"2026-03","through":"2026-05","truncated":false},
"currencies":[{"currency":"EUR","still_due":{"amount":"100.00","currency":"EUR"},"overdue":{"amount":"0.00","currency":"EUR"},"upcoming":{"amount":"100.00","currency":"EUR"},"planned_outflows":{"total":{"amount":"0.00","currency":"EUR"},"deferred_in":{"amount":"0.00","currency":"EUR"},"deferred_beyond":{"amount":"0.00","currency":"EUR"},"adjustments":{"amount":"0.00","currency":"EUR"},"missed_count":0,"missed_outstanding":{"amount":"0.00","currency":"EUR"}},"overdue_count":0,"unconverted_evidence_count":0,"balance":{"amount":"-5.50","currency":"EUR"},"balance_less_still_due":null,"account_count":1,"stale_account_count":1,"oldest_balance_obtained_at":null,"overdue_months":[],"balance_as_of":null,"payments_after_balance":null},
{"currency":"USD","still_due":{"amount":"7.00","currency":"USD"},"overdue":{"amount":"0","currency":"USD"},"upcoming":{"amount":"7.00","currency":"USD"},"planned_outflows":null,"overdue_count":0,"unconverted_evidence_count":0,"balance":null,"balance_less_still_due":null,"account_count":0,"stale_account_count":0,"oldest_balance_obtained_at":null,"overdue_months":[],"balance_as_of":null,"payments_after_balance":null}],
"balances":{"shown":true,"note":"n","stale_after_seconds":null},"accounts":[],"converted":null,"planned_outflows":{"included":false,"note":"n"},"freshness":{"computed_at":"2026-03-10T00:00:00Z","occurrences_changed_at":null}}`))
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":"not_found","message":"nothing here","request_id":"R"}`))
	}
}

func newFakeReports(t *testing.T) (*fakeReports, map[string]func() (tfprotov6.ProviderServer, error)) {
	t.Helper()
	requireTerraform(t)
	f := &fakeReports{}
	srv := httptest.NewServer(withAPIDocument(f))
	t.Cleanup(srv.Close)
	t.Setenv("LEDGER_ENDPOINT", srv.URL)
	t.Setenv("LEDGER_TOKEN", "unit-token")
	return f, map[string]func() (tfprotov6.ProviderServer, error){"ledger": providerserver.NewProtocol6WithError(New("test")())}
}

func TestReportDataSources(t *testing.T) {
	f, factories := newFakeReports(t)
	cfg := `
data "ledger_me" "me" {}

data "ledger_occurrences" "march" {
  month = "2026-03"
}

data "ledger_occurrences" "capped" {
  month     = "2026-03"
  max_items = 1
}

data "ledger_audit_entries" "recent" {
  action_prefix = "commitment."
  actor_kind    = ["token"]
}

data "ledger_outlook" "o" {
  through = "2026-05"
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{{Config: cfg, Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr("data.ledger_me.me", "role", "administrator"),
			resource.TestCheckResourceAttr("data.ledger_me.me", "grants.#", "2"),
			resource.TestCheckResourceAttr("data.ledger_occurrences.march", "items.#", "2"),
			resource.TestCheckResourceAttr("data.ledger_occurrences.march", "truncated", "false"),
			resource.TestCheckResourceAttr("data.ledger_occurrences.march", "items.0.amount.amount", "850.10"),
			resource.TestCheckResourceAttr("data.ledger_occurrences.march", "items.0.amount.currency", "EUR"),
			// The person's override and the status derived from evidence are separate attributes.
			resource.TestCheckResourceAttr("data.ledger_occurrences.march", "items.0.payment.status", "paid"),
			resource.TestCheckResourceAttr("data.ledger_occurrences.march", "items.0.payment.derived_status", "overdue"),
			resource.TestCheckResourceAttr("data.ledger_occurrences.march", "items.0.payment.override.reason", "paid in cash"),
			resource.TestCheckResourceAttr("data.ledger_occurrences.march", "items.0.payment.unconverted_currencies.0", "USD"),
			resource.TestCheckResourceAttr("data.ledger_occurrences.capped", "items.#", "1"),
			resource.TestCheckResourceAttr("data.ledger_occurrences.capped", "truncated", "true"),
			resource.TestCheckResourceAttr("data.ledger_audit_entries.recent", "items.0.details", `{"n":12345678901234567890}`),
			resource.TestCheckNoResourceAttr("data.ledger_audit_entries.recent", "items.0.actor_user_id"),
			// Each currency keeps its own amounts: nothing is summed or converted.
			resource.TestCheckResourceAttr("data.ledger_outlook.o", "currencies.#", "2"),
			resource.TestCheckResourceAttr("data.ledger_outlook.o", "currencies.0.balance.amount", "-5.50"),
			resource.TestCheckResourceAttr("data.ledger_outlook.o", "currencies.1.still_due.currency", "USD"),
			resource.TestCheckResourceAttr("data.ledger_outlook.o", "currencies.1.overdue.amount", "0"),
			resource.TestCheckNoResourceAttr("data.ledger_outlook.o", "currencies.1.balance.amount"),
			resource.TestCheckResourceAttr("data.ledger_outlook.o", "notice", "Not a guarantee."),
		)}},
	})
	f.mu.Lock()
	defer f.mu.Unlock()
	joined := strings.Join(f.queries, "\n")
	for _, want := range []string{"/api/v1/occurrences?limit=200&month=2026-03", "/api/v1/audit?action%5Bprefix%5D=commitment.&actor_kind=token&limit=200", "/api/v1/outlook?through=2026-05"} {
		if !strings.Contains(joined, want) {
			t.Errorf("no request %q in:\n%s", want, joined)
		}
	}
}

func TestReportDataSourceSurfacesAPIErrorAndValidatesInput(t *testing.T) {
	_, factories := newFakeReports(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: `data "ledger_payment_match" "m" { id = "` + matchID + `" }`, ExpectError: regexp.MustCompile(`Reading payment_match failed[\s\S]*not_found`)},
			{Config: `data "ledger_occurrences" "m" { month = "2026-13" }`, ExpectError: regexp.MustCompile(`must be a month`)},
			{Config: `data "ledger_audit_entries" "m" { max_items = 0 }`, ExpectError: regexp.MustCompile(`between 1 and 10000`)},
		},
	})
}
