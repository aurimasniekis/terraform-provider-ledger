# ADR-0012: Read-only bank data sources

Status: Accepted (2026-11-22)

## Context

Task F6 of the coverage inventory (ADR-0005) adds read-only data sources for the bank side of Ledger: the provider applications the
deployment is configured with, the banks an application offers, and the bank connections with their status and freshness. A
configuration may want to depend on them (for example to output which connection needs a new consent). ADR-0005 allows the status
and freshness of what is connected and nothing beyond what the API's connection resource already returns; it keeps bank facts
(accounts, balances, transactions) and consent flows out of the provider.

## Decision

1. **Data sources, all read only.** `ledger_bank_applications` (`GET /bank-applications`), `ledger_bank_institutions`
   (`GET /bank-applications/{application}/institutions?country=`, both arguments required), `ledger_bank_connections` (every page of
   `GET /bank-connections`, optional `status` filter applied by the provider) and `ledger_bank_connection` (by `id`). The singular one is
   the by-identifier lookup ADR-0005 lists for `GET /bank-connections/{id}`. There is nothing to import: these are not resources.
   They issue only GET requests (a unit test fails if one sends anything else) and need the `connections:read` grant.
2. **No consent, no refresh.** Starting, completing, reconnecting, refreshing and disconnecting a connection stay exceptions (ADR-0005):
   consent needs the person at the bank. The provider never calls them. Reading changes nothing at the bank.
3. **What reaches state.** For a connection: identifier, provider, application, mode, environment, account access, institution, status,
   `needs_reconnect`, the overall freshness, the consent window, the last error (kind and the provider's code only, never a payload), the
   latest refresh run, the scheduler's view, `as_of` and version. For each account: identifier, name, product, currency,
   `no_longer_reported`, `transactions_through` and its freshness (state, `stale`, last success and attempt, last error, notice code).
   **Not read at all:** balances and the account identifier hint (the last four characters of the number). The client does not decode
   them, so they cannot reach state, plan output or logs; this is stricter than ADR-0005 requires, because a balance in state changes on
   every refresh and state is often stored widely. Neither are session references, authorisation codes or credentials, which the API does not return.
   Transactions are not part of these endpoints and are not exposed. A unit test fakes an answer with a balance and a hint and
   fails when either appears anywhere in state.
4. **Freshness stays visible, never smoothed.** The API's freshness states (`fresh`, `stale`, `failed`, `reconnect_required`,
   `disconnected`, `never_refreshed`) and `as_of` are passed through unchanged, so a stale or failed connection reads as one. The
   documentation says these values move with time and that a plan reading them is re-evaluated every time. They are bank-sourced facts
   kept apart from the person's own corrections.
5. **Mode terminology is the provider's.** `mode` is `app` or `developer` exactly as the API reports it from the provider's
   documentation; it is documented as neither production nor sandbox, which is the separate `environment` attribute.
6. **Naming.** `provider` is a reserved word in Terraform configuration, so the provider-name attribute is `provider_name`.
7. **Institutions ask the provider.** The read depends on the provider being reachable. A `bank_unavailable` or `bank_rate_limited`
   answer fails the read with that code in the message; the provider's own retries cover transient HTTP failures (ADR-0009) and nothing
   more. An unknown application is reported as such; a country that is not two capitals is refused at validation.
8. **Acceptance tests** run against the pinned local Ledger, which has no provider application and no connection (connecting needs a
   bank), so they prove the empty lists and the not-found answers. The populated shapes, pagination, filtering, freshness and the
   absence of bank facts are proven offline against a fake.

## Consequences

- Nothing here can alter or leak what the bank reported beyond status and freshness. If a later need for balances arises, it is a new
  ADR with its own state-sensitivity decision.
- An account's name is a bank-supplied label and is stored in state; it is documented as such.
