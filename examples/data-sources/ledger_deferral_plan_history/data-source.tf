data "ledger_deferral_plan_history" "hosting" {
  deferral_plan_id = ledger_deferral_plan.hosting.id
}

output "plan_changes" {
  value = [for e in data.ledger_deferral_plan_history.hosting.events : "${e.at} ${e.action}"]
}
