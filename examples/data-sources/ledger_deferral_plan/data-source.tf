data "ledger_deferral_plan" "hosting" {
  id = ledger_deferral_plan.hosting.id
}

# Live state, derived by Ledger on every read from payment evidence and dates.
output "plan_missed" {
  value = data.ledger_deferral_plan.hosting.missed ? "plan date passed, still owed (${data.ledger_deferral_plan.hosting.missed_reason})" : "on plan"
}
