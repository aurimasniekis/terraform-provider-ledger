resource "ledger_bucket_member" "rent" {
  bucket_id   = ledger_bucket.fixed_costs.id
  object_type = "commitment"
  object_id   = "01JD3Y4C8V9M2Q7W5R6T0XZBNA" # a commitment identifier
}
