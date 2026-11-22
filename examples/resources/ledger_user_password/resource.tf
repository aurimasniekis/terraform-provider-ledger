# The password is write-only: read from the configuration when it is set, never stored in the plan or the state.
# Needs Terraform 1.11 or later and the root token of the root account. Supply the value from an ephemeral source.
variable "alex_password" {
  type      = string
  sensitive = true
  ephemeral = true
}

resource "ledger_user_password" "reader" {
  user_id     = ledger_user.reader.id
  password_wo = var.alex_password

  # Raise this number to set the password again (changing password_wo alone does nothing).
  password_wo_version = 1
}
