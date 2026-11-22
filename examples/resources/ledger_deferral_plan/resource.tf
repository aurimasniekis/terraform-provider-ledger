# Find the occurrence first: Ledger derives occurrences from the commitment's schedule, so it is looked up, not declared.
data "ledger_occurrence" "september" {
  commitment_id = ledger_commitment.hosting.id
  month         = "2026-09"
}

# A plan is your own record, never payment: the occurrence stays outstanding (and overdue past its due date).
resource "ledger_deferral_plan" "hosting" {
  occurrence_id = data.ledger_occurrence.september.id
  plan_type     = "personal" # or creditor_agreed, documented_grace
  planned_date  = "2026-09-20"
  reason        = "waiting for the invoice to be paid by a client"
}
