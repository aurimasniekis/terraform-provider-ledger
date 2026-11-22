# Cached EUR to USD rates since the start of the year. Each rate keeps its provider, dates and staleness.
data "ledger_exchange_rates" "eur_usd" {
  base     = "EUR"
  quote    = "USD"
  date_gte = "2026-01-01"
}

output "stale_rates" {
  value = [for r in data.ledger_exchange_rates.eur_usd.items : r.date if r.stale]
}
