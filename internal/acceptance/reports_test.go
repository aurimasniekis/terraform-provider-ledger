package acceptance

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// TestReadOnlyReportDataSources reads every report data source of ADR-0011 from a local Ledger. The server has no bank
// connection, so the bank-sourced parts are empty; the point is that each answer decodes into its schema and that the
// amounts of a commitment keep their exact text and currency.
func TestReadOnlyReportDataSources(t *testing.T) {
	merchantFixture(t)
	cfg := `
resource "ledger_commitment" "rent" {
  name       = "Rent"
  kind       = "service"
  amount     = "850.10"
  currency   = "EUR"
  start_date = "2026-01-01"
  schedule = {
    frequency = "monthly"
    interval  = 1
    day       = 5
  }
}

resource "ledger_commitment" "vps" {
  name       = "VPS"
  kind       = "server"
  amount     = "7.00"
  currency   = "USD"
  start_date = "2026-01-01"
  schedule = {
    frequency = "monthly"
    interval  = 1
    day       = 9
  }
}

data "ledger_me" "me" {}
data "ledger_grants" "all" {}
data "ledger_auth_methods" "methods" {}
data "ledger_merchant_sources" "all" {}
data "ledger_rates_status" "rates" {}

data "ledger_exchange_rates" "all" {
  max_items = 10
}

data "ledger_occurrences" "march" {
  month      = "2026-03"
  depends_on = [ledger_commitment.rent, ledger_commitment.vps]
}

data "ledger_occurrence" "first" {
  id = data.ledger_occurrences.march.items[0].id
}

data "ledger_monthly_summary" "march" {
  month      = "2026-03"
  depends_on = [ledger_commitment.rent, ledger_commitment.vps]
}

data "ledger_outlook" "next" {
  through    = "2026-06"
  depends_on = [ledger_commitment.rent, ledger_commitment.vps]
}

data "ledger_payment_matches" "all" {}

data "ledger_audit_entries" "commitments" {
  action_prefix = "commitment."
  depends_on    = [ledger_commitment.rent]
}
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: providerFactories,
		Steps: []resource.TestStep{
			{Config: `data "ledger_occurrences" "bad" { month = "2026-3" }`, PlanOnly: true, ExpectError: regexp.MustCompile("must be a month")},
			{Config: cfg, Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.ledger_me.me", "role", "administrator"),
				resource.TestCheckResourceAttrSet("data.ledger_me.me", "grants.#"),
				resource.TestCheckResourceAttrSet("data.ledger_grants.all", "resources.#"),
				resource.TestCheckResourceAttr("data.ledger_grants.all", "roles.#", "2"),
				resource.TestCheckResourceAttr("data.ledger_auth_methods.methods", "password.enabled", "true"),
				resource.TestCheckResourceAttrSet("data.ledger_merchant_sources.all", "sources.#"),
				resource.TestCheckResourceAttrSet("data.ledger_rates_status.rates", "enabled"),
				resource.TestCheckResourceAttr("data.ledger_exchange_rates.all", "truncated", "false"),
				resource.TestCheckResourceAttr("data.ledger_occurrences.march", "items.#", "2"),
				resource.TestCheckResourceAttrPair("data.ledger_occurrence.first", "commitment_id", "data.ledger_occurrences.march", "items.0.commitment_id"),
				resource.TestCheckResourceAttrSet("data.ledger_occurrence.first", "payment.status"),
				resource.TestCheckResourceAttr("data.ledger_monthly_summary.march", "month", "2026-03"),
				// One total per currency: the two commitments are never merged into one figure.
				resource.TestCheckResourceAttr("data.ledger_monthly_summary.march", "totals.#", "2"),
				resource.TestCheckResourceAttrSet("data.ledger_outlook.next", "currencies.#"),
				resource.TestCheckResourceAttrSet("data.ledger_outlook.next", "window.through"),
				resource.TestCheckResourceAttrSet("data.ledger_outlook.next", "notice"),
				resource.TestCheckResourceAttr("data.ledger_payment_matches.all", "items.#", "0"),
				resource.TestCheckResourceAttrSet("data.ledger_audit_entries.commitments", "items.0.action"),
			)},
		},
	})
}
