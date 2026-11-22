# Labels are many-valued: one resource per label per object.
resource "ledger_label_assignment" "rent_reviewed" {
  object_type = "commitment"
  object_id   = "01JD3Y4C8V9M2Q7W5R6T0XZBNA"
  label_id    = ledger_label.reviewed.id
}
