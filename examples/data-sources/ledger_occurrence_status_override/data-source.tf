data "ledger_occurrence_status_override" "march_rent" {
  occurrence_id = "01JD3Y4C8V9M2Q7W5R6T0XZBNA"
}

output "march_rent_overridden" {
  value = data.ledger_occurrence_status_override.march_rent.active
}
