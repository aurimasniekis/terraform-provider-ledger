# A name Ledger matches transactions to the merchant by. A name belongs to one merchant at most.
resource "ledger_merchant_alias" "grocer_branch" {
  merchant_id = ledger_merchant.grocer.id
  name        = "LIDL VILNIUS"
}
