# One connection by identifier.
data "ledger_bank_connection" "main" {
  id = "01JD3Y4C8V9M2Q7W5R6T0XZ001"
}

output "main_bank_freshness" {
  value = data.ledger_bank_connection.main.freshness
}
