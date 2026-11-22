# Totals are per currency and are never added across currencies.
data "ledger_monthly_summary" "march" {
  month = "2026-03"
}

output "outstanding" {
  value = [for t in data.ledger_monthly_summary.march.totals : "${t.outstanding.amount} ${t.outstanding.currency}"]
}
