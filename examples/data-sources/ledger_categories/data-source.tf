# The active children of one category.
data "ledger_categories" "housing_children" {
  parent_id = data.ledger_category.housing.id
  archived  = false
}

output "child_names" {
  value = [for c in data.ledger_categories.housing_children.items : c.name]
}
