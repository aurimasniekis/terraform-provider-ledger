# The payments recorded by hand for one occurrence. Amounts are exact decimal strings and are never summed or converted.
data "ledger_manual_payments" "march_rent" {
  occurrence_id = "01JD3Y4C8V9M2Q7W5R6T0XZBNA"
}

output "march_rent_payments" {
  value = [for p in data.ledger_manual_payments.march_rent.items : "undefined undefined on undefined"]
}
