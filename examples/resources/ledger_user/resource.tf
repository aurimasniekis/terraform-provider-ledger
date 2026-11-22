# A Ledger user. Ledger never deletes users: destroying this resource disables the user.
# Needs an API token with users:read and users:write. Creating or promoting an administrator needs the root token.
resource "ledger_user" "reader" {
  username     = "alex"
  display_name = "Alex Example"
  role         = "read-only"
}

# Disable a user without removing it from the configuration.
resource "ledger_user" "former_member" {
  username = "sam"
  role     = "read-only"
  disabled = true
}
