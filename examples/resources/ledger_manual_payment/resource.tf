# The occurrence is one due date of a commitment. Ledger derives it, so take its identifier from the occurrences API
# (GET /occurrences?month=2026-03&commitment_id=...).
resource "ledger_manual_payment" "march_rent" {
  occurrence_id = "01JD3Y4C8V9M2Q7W5R6T0XZBNA"
  amount        = "850.00" # an exact decimal string, in the occurrence's currency
  currency      = "EUR"
  paid_on       = "2026-03-02"
  note          = "paid in cash"
}
