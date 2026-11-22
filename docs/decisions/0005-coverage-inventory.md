# ADR-0005: Coverage inventory: every Management API object mapped to Terraform or an explicit exception

Status: Accepted (2026-11-22)

## Context

Operator decision D18 (option B) makes the provider cover **all** user-configurable settings and user-defined domain
objects of Ledger, with these inherent exceptions: bank-sourced facts (transactions, balances), anything that would bypass
bank consent, two-factor or single-sign-on interactive flows, and secrets, which are write-only (or, for the one value
Ledger generates itself, a documented state exception).

This inventory covers **every operation of the Management API as served by go-ledger `work` at the pinned commit**
(`internal/ledgertest/ledger.pin`, API 1.0.0, 118 operations on 86 paths; the count is of the OpenAPI document, which is the
contract). Each object is mapped to a resource and data source, to a read-only data source, or to an exception with its
reason. Status values: **done** (in this repository), **follow-up** (a resource or data source still to build, sized for one
worker in the list at the end), **blocked** (needs an API that go-ledger does not serve yet), **exception** (not managed
declaratively, with the reason).

Re-derive this table whenever the pin moves: an operation absent from it is a gap in coverage, not a decision.

## Mapping

### Configuration and user-defined domain objects

| Object (API) | Mapping | Status |
| --- | --- | --- |
| Categories, labels, buckets (`/categories`, `/labels`, `/buckets`, archive and unarchive) | `ledger_category`, `ledger_label`, `ledger_bucket` and data sources (ADR-0004) | done |
| Bucket members, category and label assignments (`/buckets/{id}/members`, `/organisation/...`) | `ledger_bucket_member`, `ledger_category_assignment`, `ledger_label_assignment`, `ledger_object_organisation` (ADR-0004). Merchants as an assignment target: separate task, in progress | done (merchants: in progress) |
| Commitments (`/commitments`) with schedule, amount and currency, tolerance, leeway | `ledger_commitment`, `ledger_commitment` and `ledger_commitments` data sources (ADR-0006) | done |
| Users (`/users`: create, get, list, patch role, display name, disabled) | `ledger_user` and data sources. Ledger has **no delete**: destroy disables the user (documented). Creating or promoting an administrator and re-enabling one needs the root token (D32). | follow-up F1 |
| User passwords (`PUT /users/{id}/password`) | Write-only: `ledger_user_password` with `password_wo` and `password_wo_version` (Terraform 1.11 or later). Needs the root token (D32). The value is never in plan or state. | follow-up F1 |
| API tokens, create (`POST /auth/tokens`) and revoke (`DELETE /tokens/{id}`), list and get (`/tokens`, `tokens:read`) | `ledger_api_token` and a metadata-only `ledger_api_tokens` data source. Tokens cannot be changed, so every change replaces. Creation by an API token needs the **root token of the root account** and creates tokens owned by that account (see the secrets section). | follow-up F2 |
| Deferral plans and their adjustments (`/deferral-plans`, `/adjustments`, undo, history) | `ledger_deferral_plan` (create, revise, destroy = undo) and `ledger_deferral_plan_adjustment`; history as a data source. They hang off an **occurrence**, which Ledger derives and may recreate, so a data source to find the occurrence is part of the task. | follow-up F3 |
| Manual payments (`/occurrences/{id}/manual-payments`) | `ledger_manual_payment`: a payment the person records by hand, user data and not a bank fact. Cannot be changed (replace). | follow-up F4 |
| Manual status override (`PUT/DELETE /occurrences/{id}/status-override`) | `ledger_occurrence_status_override` | follow-up F4 |
| Merchant review decisions (`PUT/DELETE /transactions/{id}/merchant/review`) | **Exception.** The operand is a bank-sourced transaction, whose identifier cannot be declared; it is a review workflow. Merchant assignment targets are the separate merchant task. | exception |
| Rules (expr-lang matching and categorisation rules) | Declarative resource wanted (D18). **No endpoint in the API**: blocked until go-ledger serves one. | blocked B1 |
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
| `GET /auth/methods` (public), `GET /auth/session`, `/auth/sessions`, `/auth/two-factor`, `/auth/oidc/link` | `ledger_auth_methods` data source is allowed (public information). The others are about the signed-in person's own session: exception. | follow-up F5 (methods only) |
| `GET /openapi.json`, `GET /openapi.yaml` | Used by the provider itself: the compatibility check that the supported API major version is served (D18), not a resource. | follow-up F7 |

### Exceptions, with the reason

| Operations | Reason |
| --- | --- |
| Bank connections: `POST /bank-connections`, `/complete`, `/{id}/reconnect`, `/{id}/refresh`, `/{id}/disconnect` | **Consent flow.** Connecting a bank needs the person at the bank's own page, and the redirect returns a one-time code. Terraform must not bypass or script consent, and a plan cannot express a flow that completes outside it. Refresh is an imperative action. The connection (a bank-sourced fact) is read through data sources only. |
| Bank-sourced facts: accounts, balances, transactions (and their merchant and match annotations) | **Bank facts.** Provider-supplied, kept separate from user corrections by design; never declared. |
| `POST /payment-matches` and `/confirm`, `/reject`, `/unmatch`, `/correct`, `/reverse`, `/unconfirm`, `/confirm-replacing`; `POST /payment-matching/runs` | **Review workflow and actions over bank facts.** Each operand is a bank transaction; the operations are decisions in a review queue and imperative runs, not state to converge. Reads are data sources (F5). |
| `POST /auth/sign-in`, `/sign-in/second-factor`, `/sign-out`, `/reauthenticate`, `PUT /auth/password`, `GET/POST /auth/recovery-sign-in` | **Interactive sign-in and a person's own credentials.** Sessions, cookies and CSRF tokens belong to a browser; a person's own password change is theirs to make. Terraform authenticates with an API token and never with a session. |
| `/auth/two-factor` (status, TOTP enrolment, confirm, recovery codes, disable) | **Two-factor.** The TOTP secret and the recovery codes are shown once to the person and must not pass through state; enrolment needs a code from the person's authenticator. |
| `/auth/oidc/authorize`, `/callback`, `/link`, `/reauthenticate` | **Single sign-on.** Browser redirects to an identity provider; the link is the person's own, made interactively. (The OIDC provider *configuration* is in the blocked list above.) |
| `GET/DELETE /auth/sessions`, `GET /auth/session` | **Sessions.** Transient per-browser state, not configuration. |
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
5. Merchant assignment is not part of this inventory's done list; it is the separate merchant task.

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

## Consequences

- The coverage claim is checkable: every operation of the pinned OpenAPI document appears above. Moving the pin means
  re-checking this table.
- Until B1 lands, the provider cannot manage Ledger's settings or rules, which D18 requires. This is reported, not hidden.
- The API-token resource keeps a secret in state by design; the guidance above is the mitigation.
