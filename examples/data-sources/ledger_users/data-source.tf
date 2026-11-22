# Enabled administrators.
data "ledger_users" "admins" {
  roles    = ["administrator"]
  disabled = false
}

output "admin_usernames" {
  value = [for u in data.ledger_users.admins.items : u.username]
}
