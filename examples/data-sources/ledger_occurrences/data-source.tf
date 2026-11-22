# The occurrences of one month. "status" is what Ledger shows; "derived_status" ignores a person's override.
data "ledger_occurrences" "march" {
  month = "2026-03"
}

output "overdue" {
  value = [for o in data.ledger_occurrences.march.items : "${o.amount.amount} ${o.amount.currency}" if o.payment.status == "overdue"]
}
