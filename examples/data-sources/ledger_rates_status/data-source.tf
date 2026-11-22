# Provenance and freshness of the cached conversion rates.
data "ledger_rates_status" "rates" {}

output "rates_last_success" {
  value = data.ledger_rates_status.rates.last_success_at
}
