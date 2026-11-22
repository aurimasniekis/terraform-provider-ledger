# Who the provider acts as, and what its token may do.
data "ledger_me" "self" {}

output "token_role" {
  value = data.ledger_me.self.role
}
