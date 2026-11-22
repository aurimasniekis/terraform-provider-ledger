# By name (ignoring case); parent_id = "none" restricts the search to top-level categories.
data "ledger_category" "housing" {
  name      = "housing"
  parent_id = "none"
}

# Or by identifier.
data "ledger_category" "by_id" {
  id = "01JD3Y4C8V9M2Q7W5R6T0XZBNA"
}
