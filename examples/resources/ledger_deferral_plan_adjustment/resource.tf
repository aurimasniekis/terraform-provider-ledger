# An explicit fee on a plan: a plan never adds one by itself. Amounts are exact decimal strings in the plan's currency.
resource "ledger_deferral_plan_adjustment" "late_fee" {
  deferral_plan_id = ledger_deferral_plan.hosting.id
  kind             = "fee" # or interest, other
  amount           = "5.00"
  currency         = ledger_deferral_plan.hosting.currency
  note             = "late payment fee"

  # Your own marker that you paid the charge. Reversible, and not payment of the occurrence.
  paid = false
}
