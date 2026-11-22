# Recorded commitments still due next to bank balances. Not a guarantee of money that is safe to spend.
data "ledger_outlook" "next" {
  through = "2026-06"
}

output "still_due" {
  value = [for c in data.ledger_outlook.next.currencies : "${c.still_due.amount} ${c.still_due.currency}"]
}
