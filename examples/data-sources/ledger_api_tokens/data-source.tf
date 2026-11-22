# Active tokens of one user, e.g. to audit what exists. Metadata only; needs the tokens:read grant.
data "ledger_users" "root" {
  roles = ["administrator"]
}

data "ledger_api_tokens" "active" {
  owner_id = data.ledger_users.root.items[0].id
  statuses = ["active"]
}

output "active_token_names" {
  value = [for t in data.ledger_api_tokens.active.items : t.name]
}
