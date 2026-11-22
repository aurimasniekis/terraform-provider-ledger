# A top-level category and a child of it. Categories have two levels: a child cannot be a parent.
resource "ledger_category" "housing" {
  name = "Housing"
}

resource "ledger_category" "rent" {
  name      = "Rent"
  parent_id = ledger_category.housing.id
}

# A parent cannot be archived while it still has active children (Ledger answers 409). Archive the children first, apply,
# and only then set archived = true on the parent: setting it on both in one apply fails.

# Archive instead of destroying a category that is still assigned: archiving keeps every historical assignment.
resource "ledger_category" "old_utilities" {
  name      = "Utilities (old)"
  parent_id = ledger_category.housing.id
  archived  = true
}
