# "paid" needs payment evidence (a manual payment or a confirmed bank match), so order it after the payment.
resource "ledger_occurrence_status_override" "march_rent" {
  occurrence_id = ledger_manual_payment.march_rent.occurrence_id
  status        = "paid"
  reason        = "Part payment accepted by the landlord"
  depends_on    = [ledger_manual_payment.march_rent]
}
