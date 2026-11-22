# A merchant record: a stable identifier, a display name and the aliases transactions are matched by.
# Needs an API token with the administrator-only merchants:read and merchants:write grants.
resource "ledger_merchant" "grocer" {
  display_name = "Lidl"
}

# Archive instead of destroying when decisions or assignments refer to the merchant.
resource "ledger_merchant" "closed_shop" {
  display_name = "Corner shop"
  archived     = true
}
