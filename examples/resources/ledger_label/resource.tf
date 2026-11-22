resource "ledger_label" "reviewed" {
  name = "Reviewed"
}

# Destroying a label that is still on objects fails with definition_in_use unless reassign_to names an active label
# the assignments move to (they become the person's own).
resource "ledger_label" "legacy" {
  name        = "Legacy"
  reassign_to = ledger_label.reviewed.id
}
