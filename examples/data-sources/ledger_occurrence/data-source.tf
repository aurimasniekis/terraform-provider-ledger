# By commitment and reporting month (a commitment has at most one occurrence in a month) ...
data "ledger_occurrence" "september" {
  commitment_id = ledger_commitment.hosting.id
  month         = "2026-09"
}

# ... or by identifier.
data "ledger_occurrence" "known" {
  id = "01JD3Y4C8V9M2Q7W5R6T0XZBNC"
}

# The payment state is derived by Ledger on every read; it is not a guarantee of anything.
output "september_status" {
  value = "${data.ledger_occurrence.september.status}: ${data.ledger_occurrence.september.outstanding_amount} ${data.ledger_occurrence.september.currency} outstanding"
}
