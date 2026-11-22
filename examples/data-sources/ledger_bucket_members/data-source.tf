data "ledger_bucket_members" "fixed_costs" {
  bucket_id = data.ledger_bucket.fixed_costs.id
}
