# By username (or by id).
data "ledger_user" "owner" {
  username = "alex"
}

output "owner_role" {
  value = data.ledger_user.owner.role
}
