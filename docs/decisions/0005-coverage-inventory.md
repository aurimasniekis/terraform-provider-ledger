# ADR-0005: Coverage inventory: every Management API object mapped to Terraform or an explicit exception

Status: Accepted (2026-11-22)

## Context

Operator decision D18 (option B) makes the provider cover **all** user-configurable settings and user-defined domain
objects of Ledger, with these inherent exceptions: bank-sourced facts (transactions, balances), anything that would bypass
bank consent, two-factor or single-sign-on interactive flows, and secrets, which are write-only (or, for the one value
Ledger generates itself, a documented state exception).

This inventory covers **every operation of the Management API as served by go-ledger `work` at commit
`35f80acbfbd9441560e645b00d1d160db7297885`** (API 1.0.0, **131 operations on 96 paths**; the count is of the OpenAPI document,
`internal/httpapi/openapi.yaml`, which is the contract). The acceptance harness pin (`internal/ledgertest/ledger.pin`) is a
separate, older commit that serves 118 operations on 86 paths (it lacks the merchant, `/merchants/sources` and adjustment
paid and unpaid operations); the pin moves with the merchant resources (ADR-0007). The inventory is re-derived against `work`,
not against the pin, so that it never lags what the API serves. Each object is mapped to a resource and data source, to a read-only data source, or to an exception with its
reason. Status values: **done** (in this repository), **follow-up** (a resource or data source still to build, sized for one
worker in the list at the end), **blocked** (needs an API that go-ledger does not serve yet), **exception** (not managed
declaratively, with the reason).

Re-derive this table whenever go-ledger's API changes: an operation absent from it is a gap in coverage, not a decision. The
**operation index** at the end lists every operation literally, one row each, and `make inventory-check` compares it with the
OpenAPI document of a go-ledger checkout at the commit named above (see Decision 6).

## Mapping

### Configuration and user-defined domain objects

| Object (API) | Mapping | Status |
| --- | --- | --- |
| Categories, labels, buckets (`/categories`, `/labels`, `/buckets`, archive and unarchive) | `ledger_category`, `ledger_label`, `ledger_bucket` and data sources (ADR-0004) | done |
| Bucket members, category and label assignments (`/buckets/{id}/members`, `/organisation/...`) | `ledger_bucket_member`, `ledger_category_assignment`, `ledger_label_assignment`, `ledger_object_organisation` (ADR-0004). Merchants as an assignment target: covered in the next row | done |
| Merchants (`/merchants`, `/merchants/{id}`, archive and unarchive, `/merchants/{id}/aliases`) and merchants as an assignment target | `ledger_merchant`, `ledger_merchant_alias`, `ledger_merchant` and `ledger_merchants` data sources, merchant as a target of the organisation resources (ADR-0007, PR #8). `POST /merchants/{id}/merge` is an **action exception** (see below). `GET /merchants/sources` is a read-only data source, `ledger_merchant_sources` (F5). | done in PR #8; sources: follow-up F5 |
| Commitments (`/commitments`) with schedule, amount and currency, tolerance, leeway | `ledger_commitment`, `ledger_commitment` and `ledger_commitments` data sources (ADR-0006) | done |
| Users (`/users`: create, get, list, patch role, display name, disabled) | `ledger_user` and data sources. Ledger has **no delete**: destroy disables the user (documented). Creating or promoting an administrator and re-enabling one needs the root token (D32). | follow-up F1 |
| User passwords (`PUT /users/{id}/password`) | Write-only: `ledger_user_password` with `password_wo` and `password_wo_version` (Terraform 1.11 or later). Needs the root token (D32). The value is never in plan or state. | follow-up F1 |
| API tokens, create (`POST /auth/tokens`) and revoke (`DELETE /tokens/{id}`), list and get (`/tokens`, `tokens:read`) | `ledger_api_token` and a metadata-only `ledger_api_tokens` data source. Tokens cannot be changed, so every change replaces. Creation by an API token needs the **root token of the root account** and creates tokens owned by that account (see the secrets section). | follow-up F2 |
| Deferral plans and their adjustments (`/deferral-plans`, `/adjustments`, `/adjustments/{adjustment_id}/paid` and `/unpaid`, undo, history) | `ledger_deferral_plan` (create, revise, destroy = undo) and `ledger_deferral_plan_adjustment`, whose `paid` attribute calls the paid and unpaid operations (marking an adjustment paid is the person's own record, not a bank fact); history as a data source. They hang off an **occurrence**, which Ledger derives and may recreate, so a data source to find the occurrence is part of the task. | follow-up F3 |
| Manual payments (`/occurrences/{id}/manual-payments`) | `ledger_manual_payment`: a payment the person records by hand, user data and not a bank fact. Cannot be changed (replace). | follow-up F4 |
| Manual status override (`PUT/DELETE /occurrences/{id}/status-override`) | `ledger_occurrence_status_override` | follow-up F4 |
| Merchant review decisions (`PUT /transactions/{id}/merchant/review`, `DELETE /transactions/{id}/merchant/review`) | **Exception.** A user correction over a bank fact, but a configuration cannot find its operand: **the API has no transaction list or search endpoint**, so a transaction identifier cannot be obtained or declared, and the operations are decisions in a review queue. Revisit when the API serves a way to find transactions. | exception |
| Rules (expr-lang matching and categorisation rules) | Declarative resource wanted (D18). **No endpoint in the API**: blocked until go-ledger serves one. | blocked B1 |

The B1 group below (settings, rules, provider and model settings, authentication options, bank integration settings, Pulse and
operations) is **being designed in go-ledger, ADR-0066 (PR #163)**. It is a sequencing dependency on that API work and is **not
descoped**: when its endpoints land in the OpenAPI document, the provider resources follow and this inventory is re-derived.

| Provider and model settings (optional TypeSafe and LLM suggestion adapters, consent record) | Declarative resource wanted. **No endpoint in the API.** Secrets inside are write-only. Enabling the TypeSafe adapter needs the operator's recorded acceptance and the documented field list. | blocked B1 |
| Application and planning settings (reporting time zone, freshness thresholds, retention, matching defaults, outbox and rate settings) | Declarative resource wanted. **No endpoint in the API**: today these are Ledger's own configuration (YAML, environment or `.env`), outside this provider. | blocked B1 |
| Authentication options (`auth.require_2fa`, sign-in limits, OIDC provider configuration) | Declarative resource wanted. **No endpoint in the API.** OIDC client secrets would be write-only. | blocked B1 |
| Bank integration settings (provider applications, Enable Banking app and developer mode, credentials) | Declarative resource wanted. **No endpoint in the API**; `GET /bank-applications` only lists what the server configured. Key material is write-only when an endpoint exists. Never put banking credentials in Terraform. | blocked B1 |
| Pulse event publishing, observability and other operational settings | Declarative resource wanted. **No endpoint in the API.** | blocked B1 |

### Derived views and reference data (read-only)

| Object (API) | Mapping | Status |
| --- | --- | --- |
| `GET /me`, `GET /grants` | Data sources `ledger_me` and `ledger_grants`: who the provider acts as, and the grant vocabulary (useful for token grants). | follow-up F5 |
| `GET /rates`, `GET /rates/status` | Data sources `ledger_exchange_rates` and `ledger_rates_status` (provenance and freshness stay visible). `POST /rates/refreshes` is an action: exception. | follow-up F5 |
| `GET /occurrences`, `/occurrences/{id}`, `/occurrences/summary`, `GET /outlook` | Data sources `ledger_occurrences`, `ledger_monthly_summary`, `ledger_outlook`. These are derived reports, not configuration; they stay read-only. The outlook is never a guarantee of money that is safe to spend, and the data source documentation says so. | follow-up F5 |
| `GET /payment-matches`, `/payment-matches/{id}` | Data source `ledger_payment_matches`, read-only. | follow-up F5 |
| `GET /audit` | Data source `ledger_audit_entries` (filters, paging). | follow-up F5 |
| `GET /bank-applications`, `GET /bank-applications/{application}/institutions`, `GET /bank-connections`, `GET /bank-connections/{id}` | Data sources `ledger_bank_applications`, `ledger_bank_institutions`, `ledger_bank_connections`: status and freshness of what is connected, so a configuration can depend on it. No account numbers or balances are exposed beyond what the API's connection resource already returns, and the docs say they are bank-sourced facts. | follow-up F6 |
| `GET /auth/methods` (public) | `ledger_auth_methods` data source (public information). | follow-up F5 |
| `GET /merchants/sources` | `ledger_merchant_sources` data source: the vocabulary of merchant sources. | follow-up F5 |
| `GET /transactions/{id}/merchant` | **Exception.** The merchant annotation of one bank transaction, a bank fact whose operand a configuration cannot find (no transaction list or search endpoint). Merchants themselves are read through `ledger_merchant` data sources. | exception |
| `GET /openapi.json`, `GET /openapi.yaml` | Used by the provider itself: the compatibility check that the supported API major version is served (D18), not a resource. | follow-up F7 |

### Exceptions, with the reason

| Operations | Reason |
| --- | --- |
| Bank connections: `POST /bank-connections`, `/complete`, `/{id}/reconnect`, `/{id}/refresh`, `/{id}/disconnect` | **Consent flow.** Connecting a bank needs the person at the bank's own page, and the redirect returns a one-time code. Terraform must not bypass or script consent, and a plan cannot express a flow that completes outside it. Refresh is an imperative action. The connection (a bank-sourced fact) is read through data sources only. |
| Bank-sourced facts: accounts, balances, transactions (and their match annotations; `GET /transactions/{id}/merchant` is named above) | **Bank facts.** Provider-supplied, kept separate from user corrections by design; never declared. |
| `POST /payment-matches` and `/confirm`, `/reject`, `/unmatch`, `/correct`, `/reverse`, `/unconfirm`, `/confirm-replacing`; `POST /payment-matching/runs` | **Review workflow and actions over bank facts.** The match decisions are user corrections, but **the API has no transaction list or search endpoint, so a configuration cannot find the transaction operand** of `POST /payment-matches`, and the others are decisions on a queue of candidate matches and an imperative run, not state to converge. Reads are data sources (F5). Revisit if the API gains a way to find transactions. |
| `POST /merchants/{id}/merge` | **Action.** Merging folds one merchant into another and rewrites what refers to it; it is an event, not state to converge. Declare the merchants that should exist; merge them through the UI or API. |
| `PUT /transactions/{id}/merchant/review`, `DELETE /transactions/{id}/merchant/review`, `GET /transactions/{id}/merchant` | Merchant review decisions and the merchant annotation of a bank transaction: no transaction list or search endpoint, see the rows above. |
| `POST /auth/sign-in`, `/sign-in/second-factor`, `/sign-out`, `/reauthenticate`, `PUT /auth/password`, `GET/POST /auth/recovery-sign-in` | **Interactive sign-in and a person's own credentials.** Sessions, cookies and CSRF tokens belong to a browser; a person's own password change is theirs to make. Terraform authenticates with an API token and never with a session. |
| `/auth/two-factor` (status, TOTP enrolment, confirm, recovery codes, disable) | **Two-factor.** The TOTP secret and the recovery codes are shown once to the person and must not pass through state; enrolment needs a code from the person's authenticator. |
| `/auth/oidc/authorize`, `/callback`, `/link`, `/reauthenticate` | **Single sign-on.** Browser redirects to an identity provider; the link is the person's own, made interactively. (The OIDC provider *configuration* is in the blocked list above.) |
| `GET /auth/session`, `GET /auth/sessions`, `DELETE /auth/sessions/{id}` | **Sessions.** Transient per-browser state, not configuration; ending a session is the person's own action. |
| `GET/DELETE /auth/tokens`, `GET /auth/tokens/{id}` (session-only routes) | The session-only twins of `/tokens`. The provider authenticates with a token and so cannot use them; `/tokens` is used instead (F2). |
| `POST /mcp` | Protocol endpoint of the Model Context Protocol server, not a configuration object. |
| `POST /rates/refreshes` | Imperative action (refresh now); rates are provider-sourced data. |

## Decision

1. The table above is the coverage inventory. Resources are added by object, in the follow-up items below, each with import,
   docs, examples, offline unit tests and acceptance tests against the pinned local Ledger.
2. **Settings the API does not serve are not invented.** They are listed as blocked (B1) and the provider must not write
   Ledger's configuration files or call undocumented routes. Control raises the API work in go-ledger; the resource task
   follows when the endpoint and its OpenAPI description exist. This is a sequencing dependency, not a descope: the objects
   stay in scope (D18).
3. **State and secrets guidance** (also in the provider documentation as it grows):
   - Secrets are write-only (`*_wo` with `*_wo_version`, Terraform 1.11 or later) wherever Ledger accepts a value from the
     client. They never appear in plan or state, and never in a set.
   - **The one exception** is the API token that Ledger generates (D21): it is returned once, held as a `Computed`,
     `Sensitive` attribute and **is stored in Terraform state**. Protect and encrypt state, restrict who can read it, and
     prefer short-lived tokens with `expires_at`. Replacing the resource creates a new token and revokes the old one.
   - The provider's own token comes from the environment or a token file, not from a configuration literal (ADR-0001).
   - Never place banking credentials, account numbers, bank payloads, TOTP secrets or recovery codes in Terraform
     configuration, variables or state.
4. **Interactive prerequisites.** These things need a person first and Terraform cannot do them:
   - Create the first user and its **root token** on the host with Ledger's own command (`ledger token create --create
     administrator --root`); the provider then uses a token.
   - Sign in with a password (and second factor) to use any session-only route; the provider cannot.
   - Complete bank consent in the browser; the connection then exists and can be read.
   - Enrol two-factor and link single sign-on, if wanted, in the Ledger UI.
   - Creating or promoting administrators, re-enabling a disabled administrator and setting any password with a token
     need the root token of the root account (D32); other tokens get `root_token_required`.
   - A token acts within its owner's current role and grants, so the provider's token must hold the grants for the objects
     it manages (`commitments:write`, `organisation:write`, `users:write`, `tokens:write`, ...).
5. Merchants are covered by ADR-0007 (PR #8): until that merges, the merchant rows are done on its branch and the inventory
   names them so that the claim stays checkable the day both land.
6. **Operation index and validator.** The index at the end has one row per operation of the OpenAPI document. `make
   inventory-check` (set `LEDGER_INVENTORY_SOURCE` to a go-ledger checkout at the commit above) fails when an operation is
   missing from the index, when the index names one the document does not serve, or when the counts disagree. `make test` runs the
   offline half: the index is well formed, has no duplicates and matches the stated counts.

## Follow-up work (one worker each)

- **F1** `ledger_user` and `ledger_user_password` (write-only) with data sources; destroy disables.
- **F2** `ledger_api_token` (state exception, replace-on-change) and `ledger_api_tokens`; root-token prerequisite tests.
- **F3** `ledger_deferral_plan`, `ledger_deferral_plan_adjustment`, history data source, and a way to find an occurrence.
- **F4** `ledger_manual_payment` and `ledger_occurrence_status_override`.
- **F5** Read-only data sources: me, grants, auth methods, exchange rates and status, occurrences, monthly summary, outlook,
  payment matches, audit entries.
- **F6** Read-only bank data sources: applications, institutions, connections (freshness and status visible).
- **F7** Compatibility check in `Configure`: read `info.version` from the served OpenAPI document, fail on an unsupported
  major, warn on a newer minor (the harness already checks the pin; the provider does not yet).
- **F8** Client reliability: idempotent-read retries (D17, at most 3 with backoff) and timeouts, shared by all resources.
- **B1** Settings, rules, provider and model settings, authentication options, bank integration settings, Pulse: **blocked on
  go-ledger API endpoints**. Control should ask for the API per group (rules; provider/model settings; app and planning
  settings; authentication options; bank integration settings; Pulse and operations) before the matching resource tasks.

## Operation index

Derived against go-ledger `work` at `35f80acbfbd9441560e645b00d1d160db7297885`: **131 operations on 96 paths**. One row per
operation; the status is the first word (`done`, `follow-up` or `exception`).

| Operation | Mapping | Status |
| --- | --- | --- |
| `GET /openapi.json` | provider's own API compatibility check, not a resource | follow-up F7 |
| `GET /openapi.yaml` | provider's own API compatibility check, not a resource | follow-up F7 |
| `GET /me` | data source `ledger_me` | follow-up F5 |
| `GET /grants` | data source `ledger_grants` | follow-up F5 |
| `GET /audit` | data source `ledger_audit_entries` | follow-up F5 |
| `GET /users` | data sources `ledger_user`, `ledger_users` | follow-up F1 |
| `POST /users` | `ledger_user` (no delete: destroy disables) | follow-up F1 |
| `GET /users/{id}` | data sources `ledger_user`, `ledger_users` | follow-up F1 |
| `PATCH /users/{id}` | `ledger_user` (no delete: destroy disables) | follow-up F1 |
| `GET /commitments` | data source `ledger_commitments` (ADR-0006) | done |
| `POST /commitments` | `ledger_commitment` and its data source (ADR-0006) | done |
| `GET /commitments/{id}` | `ledger_commitment` and its data source (ADR-0006) | done |
| `PATCH /commitments/{id}` | `ledger_commitment` and its data source (ADR-0006) | done |
| `DELETE /commitments/{id}` | `ledger_commitment` and its data source (ADR-0006) | done |
| `PUT /users/{id}/password` | `ledger_user_password`, write-only | follow-up F1 |
| `POST /auth/sign-in` | interactive sign-in or the person's own credentials (password, recovery sign-in, reauthentication) | exception |
| `POST /auth/sign-in/second-factor` | interactive sign-in or the person's own credentials (password, recovery sign-in, reauthentication) | exception |
| `POST /auth/sign-out` | interactive sign-in or the person's own credentials (password, recovery sign-in, reauthentication) | exception |
| `GET /auth/session` | session: transient per-browser state, not configuration | exception |
| `GET /auth/sessions` | session: transient per-browser state, not configuration | exception |
| `DELETE /auth/sessions/{id}` | session: ends one of the signed-in person's own browser sessions; transient state, not configuration | exception |
| `POST /auth/reauthenticate` | interactive sign-in or the person's own credentials (password, recovery sign-in, reauthentication) | exception |
| `PUT /auth/password` | interactive sign-in or the person's own credentials (password, recovery sign-in, reauthentication) | exception |
| `GET /auth/methods` | data source `ledger_auth_methods` | follow-up F5 |
| `GET /auth/oidc/authorize` | single sign-on: browser redirects to an identity provider, made interactively | exception |
| `GET /auth/oidc/callback` | single sign-on: browser redirects to an identity provider, made interactively | exception |
| `GET /auth/oidc/link` | single sign-on: browser redirects to an identity provider, made interactively | exception |
| `POST /auth/oidc/link` | single sign-on: browser redirects to an identity provider, made interactively | exception |
| `DELETE /auth/oidc/link` | single sign-on: browser redirects to an identity provider, made interactively | exception |
| `POST /auth/oidc/reauthenticate` | single sign-on: browser redirects to an identity provider, made interactively | exception |
| `GET /auth/recovery-sign-in` | interactive sign-in or the person's own credentials (password, recovery sign-in, reauthentication) | exception |
| `POST /auth/recovery-sign-in` | interactive sign-in or the person's own credentials (password, recovery sign-in, reauthentication) | exception |
| `GET /auth/two-factor` | two-factor: the TOTP secret and recovery codes are shown once to the person and must not pass through state | exception |
| `DELETE /auth/two-factor` | two-factor: the TOTP secret and recovery codes are shown once to the person and must not pass through state | exception |
| `POST /auth/two-factor/totp` | two-factor: the TOTP secret and recovery codes are shown once to the person and must not pass through state | exception |
| `POST /auth/two-factor/totp/confirm` | two-factor: the TOTP secret and recovery codes are shown once to the person and must not pass through state | exception |
| `POST /auth/two-factor/recovery-codes` | two-factor: the TOTP secret and recovery codes are shown once to the person and must not pass through state | exception |
| `GET /occurrences` | data source `ledger_occurrences` | follow-up F5 |
| `GET /occurrences/summary` | data source `ledger_monthly_summary` | follow-up F5 |
| `GET /occurrences/{id}` | data source `ledger_occurrences` (by id) | follow-up F5 |
| `GET /outlook` | data source `ledger_outlook` | follow-up F5 |
| `GET /auth/tokens` | session-only twin of `/tokens`; the provider authenticates with a token and uses `/tokens` | exception |
| `POST /auth/tokens` | `ledger_api_token` create (the one route a bearer token may use; needs the root token) | follow-up F2 |
| `GET /auth/tokens/{id}` | session-only twin of `/tokens`; the provider authenticates with a token and uses `/tokens` | exception |
| `DELETE /auth/tokens/{id}` | session-only twin of `/tokens`; the provider authenticates with a token and uses `/tokens` | exception |
| `GET /tokens` | metadata-only data source `ledger_api_tokens` | follow-up F2 |
| `GET /tokens/{id}` | metadata-only data source `ledger_api_tokens` | follow-up F2 |
| `DELETE /tokens/{id}` | `ledger_api_token` destroy (revoke) | follow-up F2 |
| `GET /occurrences/{id}/manual-payments` | data source of recorded payments, with `ledger_manual_payment` | follow-up F4 |
| `POST /occurrences/{id}/manual-payments` | `ledger_manual_payment` (replace on change) | follow-up F4 |
| `GET /occurrences/{id}/manual-payments/{payment_id}` | data source of recorded payments, with `ledger_manual_payment` | follow-up F4 |
| `DELETE /occurrences/{id}/manual-payments/{payment_id}` | `ledger_manual_payment` (replace on change) | follow-up F4 |
| `PUT /occurrences/{id}/status-override` | `ledger_occurrence_status_override` | follow-up F4 |
| `DELETE /occurrences/{id}/status-override` | `ledger_occurrence_status_override` | follow-up F4 |
| `GET /payment-matches` | data source `ledger_payment_matches` | follow-up F5 |
| `POST /payment-matches` | review workflow or run over bank transactions: the API has no transaction list or search endpoint, so a configuration cannot find the operand; decisions in a queue, not state to converge | exception |
| `GET /payment-matches/{id}` | data source `ledger_payment_matches` (by id) | follow-up F5 |
| `POST /payment-matches/{id}/confirm` | review workflow or run over bank transactions: the API has no transaction list or search endpoint, so a configuration cannot find the operand; decisions in a queue, not state to converge | exception |
| `POST /payment-matches/{id}/reject` | review workflow or run over bank transactions: the API has no transaction list or search endpoint, so a configuration cannot find the operand; decisions in a queue, not state to converge | exception |
| `POST /payment-matches/{id}/unmatch` | review workflow or run over bank transactions: the API has no transaction list or search endpoint, so a configuration cannot find the operand; decisions in a queue, not state to converge | exception |
| `POST /payment-matches/{id}/correct` | review workflow or run over bank transactions: the API has no transaction list or search endpoint, so a configuration cannot find the operand; decisions in a queue, not state to converge | exception |
| `POST /payment-matches/{id}/reverse` | review workflow or run over bank transactions: the API has no transaction list or search endpoint, so a configuration cannot find the operand; decisions in a queue, not state to converge | exception |
| `POST /payment-matches/{id}/unconfirm` | review workflow or run over bank transactions: the API has no transaction list or search endpoint, so a configuration cannot find the operand; decisions in a queue, not state to converge | exception |
| `POST /payment-matches/{id}/confirm-replacing` | review workflow or run over bank transactions: the API has no transaction list or search endpoint, so a configuration cannot find the operand; decisions in a queue, not state to converge | exception |
| `GET /bank-applications` | data source `ledger_bank_applications` | follow-up F6 |
| `GET /bank-applications/{application}/institutions` | data source `ledger_bank_institutions` | follow-up F6 |
| `GET /bank-connections` | data sources `ledger_bank_connections` | follow-up F6 |
| `POST /bank-connections` | consent flow or imperative action on a bank connection; Terraform must not script consent | exception |
| `POST /bank-connections/complete` | consent flow or imperative action on a bank connection; Terraform must not script consent | exception |
| `GET /bank-connections/{id}` | data sources `ledger_bank_connections` | follow-up F6 |
| `POST /bank-connections/{id}/reconnect` | consent flow or imperative action on a bank connection; Terraform must not script consent | exception |
| `POST /bank-connections/{id}/refresh` | consent flow or imperative action on a bank connection; Terraform must not script consent | exception |
| `POST /bank-connections/{id}/disconnect` | consent flow or imperative action on a bank connection; Terraform must not script consent | exception |
| `POST /mcp` | Model Context Protocol endpoint, not a configuration object | exception |
| `POST /payment-matching/runs` | review workflow or run over bank transactions: the API has no transaction list or search endpoint, so a configuration cannot find the operand; decisions in a queue, not state to converge | exception |
| `GET /merchants/sources` | data source `ledger_merchant_sources` (provider vocabulary of merchant sources, read-only) | follow-up F5 |
| `GET /transactions/{id}/merchant` | bank fact: the merchant annotation of a bank transaction; read through the merchant data sources of ADR-0007 where it matters | exception |
| `PUT /transactions/{id}/merchant/review` | review decision over a bank transaction: the API has no transaction list or search endpoint, so a configuration cannot find the operand | exception |
| `DELETE /transactions/{id}/merchant/review` | review decision over a bank transaction: the API has no transaction list or search endpoint, so a configuration cannot find the operand | exception |
| `GET /merchants` | `ledger_merchant`, `ledger_merchant` and `ledger_merchants` data sources (ADR-0007, PR #8) | done |
| `POST /merchants` | `ledger_merchant`, `ledger_merchant` and `ledger_merchants` data sources (ADR-0007, PR #8) | done |
| `GET /merchants/{id}` | `ledger_merchant`, `ledger_merchant` and `ledger_merchants` data sources (ADR-0007, PR #8) | done |
| `PATCH /merchants/{id}` | `ledger_merchant`, `ledger_merchant` and `ledger_merchants` data sources (ADR-0007, PR #8) | done |
| `DELETE /merchants/{id}` | `ledger_merchant`, `ledger_merchant` and `ledger_merchants` data sources (ADR-0007, PR #8) | done |
| `POST /merchants/{id}/archive` | `archived` attribute of `ledger_merchant` (ADR-0007, PR #8) | done |
| `POST /merchants/{id}/unarchive` | `archived` attribute of `ledger_merchant` (ADR-0007, PR #8) | done |
| `POST /merchants/{id}/aliases` | `ledger_merchant_alias` (ADR-0007, PR #8) | done |
| `DELETE /merchants/{id}/aliases/{alias_id}` | `ledger_merchant_alias` (ADR-0007, PR #8) | done |
| `POST /merchants/{id}/merge` | action: merging destroys one merchant and rewrites references; it is not state to converge | exception |
| `GET /rates` | data source `ledger_exchange_rates` | follow-up F5 |
| `GET /rates/status` | data source `ledger_rates_status` | follow-up F5 |
| `POST /rates/refreshes` | imperative action (refresh now); rates are provider-sourced data | exception |
| `GET /deferral-plans` | data sources `ledger_deferral_plan`, `ledger_deferral_plans` | follow-up F3 |
| `POST /deferral-plans` | `ledger_deferral_plan` (create, revise) | follow-up F3 |
| `GET /deferral-plans/{id}` | data sources `ledger_deferral_plan`, `ledger_deferral_plans` | follow-up F3 |
| `PATCH /deferral-plans/{id}` | `ledger_deferral_plan` (create, revise) | follow-up F3 |
| `POST /deferral-plans/{id}/undo` | `ledger_deferral_plan` destroy (undo) | follow-up F3 |
| `GET /deferral-plans/{id}/history` | data source of a plan's history | follow-up F3 |
| `POST /deferral-plans/{id}/adjustments` | `ledger_deferral_plan_adjustment` (create, destroy) | follow-up F3 |
| `DELETE /deferral-plans/{id}/adjustments/{adjustment_id}` | `ledger_deferral_plan_adjustment` (create, destroy) | follow-up F3 |
| `POST /deferral-plans/{id}/adjustments/{adjustment_id}/paid` | `paid` attribute of `ledger_deferral_plan_adjustment` (true calls `/paid`, false calls `/unpaid`); user data, not a bank fact | follow-up F3 |
| `POST /deferral-plans/{id}/adjustments/{adjustment_id}/unpaid` | `paid` attribute of `ledger_deferral_plan_adjustment` (true calls `/paid`, false calls `/unpaid`); user data, not a bank fact | follow-up F3 |
| `GET /categories` | `ledger_category` and data sources (ADR-0004) | done |
| `POST /categories` | `ledger_category` (ADR-0004) | done |
| `GET /categories/{id}` | `ledger_category` and data sources (ADR-0004) | done |
| `PATCH /categories/{id}` | `ledger_category` (ADR-0004) | done |
| `DELETE /categories/{id}` | `ledger_category` (ADR-0004) | done |
| `POST /categories/{id}/archive` | `archived` attribute of `ledger_category` (ADR-0004) | done |
| `POST /categories/{id}/unarchive` | `archived` attribute of `ledger_category` (ADR-0004) | done |
| `GET /labels` | `ledger_label` and data sources (ADR-0004) | done |
| `POST /labels` | `ledger_label` (ADR-0004) | done |
| `GET /labels/{id}` | `ledger_label` and data sources (ADR-0004) | done |
| `PATCH /labels/{id}` | `ledger_label` (ADR-0004) | done |
| `DELETE /labels/{id}` | `ledger_label` (ADR-0004) | done |
| `POST /labels/{id}/archive` | `archived` attribute of `ledger_label` (ADR-0004) | done |
| `POST /labels/{id}/unarchive` | `archived` attribute of `ledger_label` (ADR-0004) | done |
| `GET /buckets` | `ledger_bucket` and data sources (ADR-0004) | done |
| `POST /buckets` | `ledger_bucket` (ADR-0004) | done |
| `GET /buckets/{id}` | `ledger_bucket` and data sources (ADR-0004) | done |
| `PATCH /buckets/{id}` | `ledger_bucket` (ADR-0004) | done |
| `DELETE /buckets/{id}` | `ledger_bucket` (ADR-0004) | done |
| `POST /buckets/{id}/archive` | `archived` attribute of `ledger_bucket` (ADR-0004) | done |
| `POST /buckets/{id}/unarchive` | `archived` attribute of `ledger_bucket` (ADR-0004) | done |
| `GET /buckets/{id}/members` | data source of members | done |
| `PUT /buckets/{id}/members/{object_type}/{object_id}` | `ledger_bucket_member` (ADR-0004) | done |
| `DELETE /buckets/{id}/members/{object_type}/{object_id}` | `ledger_bucket_member` (ADR-0004) | done |
| `GET /organisation/{object_type}/{object_id}` | `ledger_object_organisation`, `ledger_category_assignment`, `ledger_label_assignment` (ADR-0004) | done |
| `PUT /organisation/{object_type}/{object_id}/category` | `ledger_object_organisation`, `ledger_category_assignment`, `ledger_label_assignment` (ADR-0004) | done |
| `DELETE /organisation/{object_type}/{object_id}/category` | `ledger_object_organisation`, `ledger_category_assignment`, `ledger_label_assignment` (ADR-0004) | done |
| `PUT /organisation/{object_type}/{object_id}/labels/{label_id}` | `ledger_object_organisation`, `ledger_category_assignment`, `ledger_label_assignment` (ADR-0004) | done |
| `DELETE /organisation/{object_type}/{object_id}/labels/{label_id}` | `ledger_object_organisation`, `ledger_category_assignment`, `ledger_label_assignment` (ADR-0004) | done |

## Consequences

- The coverage claim is checkable: every operation of the pinned OpenAPI document appears above. Moving the pin means
  re-checking this table.
- Until B1 lands, the provider cannot manage Ledger's settings or rules, which D18 requires. This is reported, not hidden.
- The API-token resource keeps a secret in state by design; the guidance above is the mitigation.
