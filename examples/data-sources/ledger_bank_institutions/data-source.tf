# The banks an application offers in a country. Ledger asks the provider, so this needs the provider to be reachable.
data "ledger_bank_applications" "all" {}

data "ledger_bank_institutions" "lithuania" {
  application = data.ledger_bank_applications.all.items[0].name
  country     = "LT"
}

output "bank_names" {
  value = [for i in data.ledger_bank_institutions.lithuania.items : i.name]
}
