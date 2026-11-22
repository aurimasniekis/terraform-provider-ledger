# Every domain commitment in euro. Totals of different currencies are never combined.
data "ledger_commitments" "eur_domains" {
  kind     = "domain"
  currency = "EUR"
}

output "domain_names" {
  value = [for c in data.ledger_commitments.eur_domains.items : c.name]
}
