data "ledger_commitment" "rent" {
  id = "01JD3Y4C8V9M2Q7W5R6T0XZBNA"
}

output "rent_amount" {
  value = "${data.ledger_commitment.rent.amount} ${data.ledger_commitment.rent.currency}"
}
