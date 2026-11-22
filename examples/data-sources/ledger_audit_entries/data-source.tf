# The latest commitment changes. Needs a token with the audit grant.
data "ledger_audit_entries" "commitments" {
  action_prefix = "commitment."
  max_items     = 50
}

output "actions" {
  value = [for e in data.ledger_audit_entries.commitments.items : e.action]
}
