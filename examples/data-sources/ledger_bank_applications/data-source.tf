# The provider applications Ledger is configured with (credentials are never shown).
data "ledger_bank_applications" "all" {}

output "bank_applications" {
  value = [for a in data.ledger_bank_applications.all.items : "${a.name}: mode ${a.mode}, ${a.environment}, access ${a.account_access}"]
}
