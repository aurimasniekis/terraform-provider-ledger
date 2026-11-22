# The person's category for one commitment (single-valued: one resource per object).
resource "ledger_category_assignment" "rent" {
  object_type = "commitment"
  object_id   = "01JD3Y4C8V9M2Q7W5R6T0XZBNA" # a commitment, account, transaction or merchant identifier
  category_id = ledger_category.rent.id
}

# Omit category_id to record that the person chose no category; rules and models respect that decision.
resource "ledger_category_assignment" "no_category" {
  object_type = "account"
  object_id   = "01JD3Y4C8V9M2Q7W5R6T0XZBNB"
}

# A merchant takes a category too (administrator-only merchants grants; an archived merchant takes no new assignment).
resource "ledger_category_assignment" "grocer" {
  object_type = "merchant"
  object_id   = ledger_merchant.grocer.id
  category_id = ledger_category.groceries.id
}
