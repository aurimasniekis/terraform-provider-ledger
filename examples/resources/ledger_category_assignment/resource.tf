# The person's category for one commitment (single-valued: one resource per object).
resource "ledger_category_assignment" "rent" {
  object_type = "commitment"
  object_id   = "01JD3Y4C8V9M2Q7W5R6T0XZBNA" # a commitment, account or transaction identifier
  category_id = ledger_category.rent.id
}

# Omit category_id to record that the person chose no category; rules and models respect that decision.
resource "ledger_category_assignment" "no_category" {
  object_type = "account"
  object_id   = "01JD3Y4C8V9M2Q7W5R6T0XZBNB"
}
