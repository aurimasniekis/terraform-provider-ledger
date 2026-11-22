# By display name (ignoring case); archived and merged merchants are found too.
data "ledger_merchant" "grocer" {
  display_name = "lidl"
}

# Or by identifier.
data "ledger_merchant" "by_id" {
  id = "01JD3Y4C8V9M2Q7W5R6T0XZBNA"
}
