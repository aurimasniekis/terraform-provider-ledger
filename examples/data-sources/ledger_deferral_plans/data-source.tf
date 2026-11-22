# Every active plan whose planned date is in September 2026.
data "ledger_deferral_plans" "september" {
  state         = "active"
  planned_month = "2026-09"
}

output "september_planned" {
  value = [for p in data.ledger_deferral_plans.september.items : "${p.outstanding_amount} ${p.currency} on ${p.planned_date}"]
}
