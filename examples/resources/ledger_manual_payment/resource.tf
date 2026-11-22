# The occurrence is one due date of a commitment. Ledger derives it, so find it with the ledger_occurrence data source.
data "ledger_occurrence" "march" {
  commitment_id = ledger_commitment.rent.id
  month         = "2026-03"
}

resource "ledger_manual_payment" "march_rent" {
  occurrence_id = data.ledger_occurrence.march.id
  amount        = "850.00" # an exact decimal string, in the occurrence's currency
  currency      = "EUR"
  paid_on       = "2026-03-02"
  note          = "paid in cash"
}
