# Everything assigned to one commitment, whoever assigned it (the person, a rule or a model).
data "ledger_object_organisation" "rent" {
  object_type = "commitment"
  object_id   = "01JD3Y4C8V9M2Q7W5R6T0XZBNA"
}

output "rent_category_origin" {
  value = data.ledger_object_organisation.rent.category_origin
}
