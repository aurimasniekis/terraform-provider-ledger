# Amounts are exact decimal strings with a currency, never numbers.
resource "ledger_commitment" "rent" {
  name            = "Rent"
  kind            = "service"
  amount          = "850.00"
  currency        = "EUR"
  start_date      = "2026-01-01"
  priority        = "essential"
  match_tolerance = "0.50" # a booked payment up to 0.50 EUR short may still be confirmed automatically

  schedule = {
    frequency = "monthly"
    interval  = 1 # every month; 3 = quarterly
    day       = 1
  }

  # What you know about the creditor's grace. Omit the block to record nothing.
  leeway = {
    kind       = "period"
    days       = 5
    conditions = "after written notice"
    source     = "lease clause 7"
  }
}

# A quarterly commitment that ends. A day past the end of a month falls on that month's last day.
resource "ledger_commitment" "vps" {
  name       = "VPS"
  kind       = "server"
  amount     = "30.00"
  currency   = "USD"
  start_date = "2026-01-31"
  end_date   = "2027-12-31"

  schedule = {
    frequency = "monthly"
    interval  = 3
    day       = 31
  }
}

# An annual commitment. February 29 falls on February 28 in years that are not leap years.
resource "ledger_commitment" "domain" {
  name       = "example.org"
  kind       = "domain"
  amount     = "12.50"
  currency   = "EUR"
  start_date = "2024-02-29"
  priority   = "flexible"

  schedule = {
    frequency = "annual"
    month     = 2
    day       = 29
  }

  leeway = {
    kind  = "deadline"
    until = "2026-12-31"
  }
}
