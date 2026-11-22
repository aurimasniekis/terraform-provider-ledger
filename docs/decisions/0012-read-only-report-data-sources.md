# ADR-0012: Read-only report data sources

Status: Accepted (2026-11-22)

## Context

ADR-0005 lists item F5: the derived views and reference data of the Management API that a configuration may read but not
manage. They are not configuration, and several carry bank-sourced facts (balances, transactions) or amounts in several
currencies, so how they are exposed matters for financial integrity.

## Decision

1. **Twelve data sources, all read-only**: `ledger_me`, `ledger_grants`, `ledger_auth_methods`, `ledger_merchant_sources`,
   `ledger_rates_status`, `ledger_exchange_rates`, `ledger_occurrences`, `ledger_monthly_summary`, `ledger_outlook`,
   `ledger_payment_matches`, `ledger_payment_match` and `ledger_audit_entries`. They need the token's read grants; an API refusal is
   shown as the error it is.
2. **Money is an object**: `{amount, currency}`, where `amount` is the exact decimal string the API sent (scale and sign
   kept). Nothing is parsed to a number, summed, rounded or converted. Totals stay one entry per currency; the outlook's
   optional `converted` block is shown only when the server produced it, with its rates, ages and exclusions.
3. **Bank facts and corrections stay apart, as the API has them**: an occurrence's `payment.status` and
   `payment.derived_status` are separate, with the person's `override` and its reason beside them; evidence counts are split
   into manual and bank; payment matches carry `origin`, `state`, who created and decided them, and the bank transaction under
   its own `transaction` object. Freshness (`freshness`, `as_of`, `stale`, rate dates, `truncated`) is never dropped.
4. **Lists** read the cursor-paged endpoints up to `max_items` (default 1000, at most 10000) and set `truncated` when the API had
   more, so a partial answer is visible and not mistaken for a full one.
5. **Open-shaped or very deep parts are the API's own JSON** in a string attribute (audit `details`, a deferral plan inside a
   summary item, the outlook's bucket slice and conversion detail); use `jsondecode()`. Everything else is typed.
6. The outlook and summary documentation says they are never a guarantee of money that is safe to spend.
7. `provider` is a reserved attribute name in Terraform, so the rate provider is `rate_provider`.
8. Implementation: each data source is a small declaration (endpoint, inputs, a tree of typed fields) in
   `internal/provider/report_definitions.go`; one node type derives the schema, the framework type and the conversion of the
   decoded answer (`report_schema.go`), and the client reads answers as raw documents with `json.Number` (`internal/client/reports.go`).

## Not covered

`ledger_bank_applications`, `ledger_bank_institutions` and `ledger_bank_connections` are F6. `POST /rates/refreshes`, payment-match
decisions and merchant merges stay exceptions (ADR-0005).

## Consequences

- A change to a response shape that the declarations do not know yields null for a missing field and an error for a wrong type;
  the acceptance tests run against the pinned Ledger.
- State holds what the API returned, including bank account names and balances in the outlook and bank text in payment matches;
  protect state accordingly.
