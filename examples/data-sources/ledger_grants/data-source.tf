# The grant vocabulary, e.g. to see which grants only administrators can hold.
data "ledger_grants" "all" {}

output "admin_only_resources" {
  value = [for r in data.ledger_grants.all.resources : r.name if r.admin_only]
}
