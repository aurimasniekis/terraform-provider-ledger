# WARNING: the token Ledger generates is stored in the Terraform state (sensitive, but not encrypted by Terraform).
# Protect and encrypt the state, prefer a short expiry and narrow grants.
#
# Creating a token needs the provider to run with the ROOT token of the root account (the one created on the Ledger
# host with: ledger token create --create administrator --root). Any other token is refused.
resource "ledger_api_token" "reporting" {
  name   = "monthly-reporting"
  grants = ["commitments:read", "organisation:read"]

  # Optional. Without it the token never expires. Ledger may cap it at the expiry of the creating token.
  expires_at = "2030-01-31T00:00:00Z"
}

# A token cannot be changed: changing any argument replaces it (the old one is revoked).
# Hand the secret on only to a sensitive place; never print it in a plain output.
output "reporting_token" {
  value     = ledger_api_token.reporting.secret
  sensitive = true
}

# A root token holds every grant of its owner's role. Prefer narrow grants.
resource "ledger_api_token" "automation_root" {
  name = "automation-root"
  root = true
}
