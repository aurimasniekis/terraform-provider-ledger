# Matches waiting for a decision. A suggestion is not proof of payment.
data "ledger_payment_matches" "suggested" {
  state = "suggested"
}

output "suggestion_count" {
  value = length(data.ledger_payment_matches.suggested.items)
}
