# ADR-0015: API tokens: the one secret kept in state

Status: Accepted (2026-11-22)

## Context

Task F2 of the coverage inventory (ADR-0005) covers Ledger's API tokens. Ledger **generates** the token and returns it once, in the
answer to `POST /auth/tokens` (field `secret`, `Cache-Control: no-store`); it keeps only a hash, and every later read serves metadata
only (a hint, a prefix and the last characters). Every other secret in this provider is write-only because the client supplies it;
this one cannot be, because Terraform has no way to receive a value that the remote system chooses and shows once, other than a
(sensitive) computed attribute. It is the documented state exception of ADR-0005, decision D18.

Creating a token with an API token needs the **root token of the root account** (the first user, created on the host by the bootstrap
command, decision D25); any other API token is refused with 403 `forbidden`. A token created this way is owned by the root account and
bounded by its role. Revoking a root token with an API token needs a root token too (403 `root_token_required`, D34); revoking any
other token needs `tokens:write`. Revocation cascades: every token created through the revoked one is revoked as well. The routes of
the session-only twins (`/auth/tokens` list, get and delete) are not used: the provider authenticates with a token.

## Decision

1. **Objects.** Resource `ledger_api_token`; data sources `ledger_api_token` (by identifier) and `ledger_api_tokens` (filters
   `owner_id`, applied by Ledger, and `statuses`, applied by the provider; every page is read). The data sources have **no secret
   attribute**.
2. **The secret is a sensitive computed attribute and is stored in state.** `secret` is `Computed` and `Sensitive`. It is saved to state
   in the same step as the creation, before anything else that could fail. It is kept in state across refreshes (Ledger cannot return
   it again) and is not marked as a plan input. The resource documentation opens with a warning that the state holds the token:
   encrypt and restrict the state, prefer `expires_at` and narrow `grants`, never print it in a non-sensitive output. `sensitive` hides
   the value from plan output only; it does not protect state.
3. **No leak elsewhere.** The client holds the secret as a `client.Secret` that prints a redaction marker in every formatting verb; it is
   revealed only to store it. Error and diagnostic text comes from Ledger's structured errors, which never carry it; nothing in the
   provider logs request or response bodies. Unit tests check the data sources for a secret attribute and the state for a match with the
   issued token only in `secret`.
4. **Replace on change.** A token cannot be changed (the API has no update), so `name`, `grants`, `root` and `expires_at` all replace.
   Replacement destroys first (revoking the old token and, by Ledger's cascade, the tokens made through it), then creates. For a zero-gap
   rotation, set `lifecycle { create_before_destroy = true }`, so the new token is created before the old one is revoked; without it, a
   create that fails (for example, because the provider does not run with the root token) leaves the old token revoked and nothing in its
   place. `Update` is a bug guard that fails.
5. **Grants or root.** `grants` (a set, at least one) or `root = true`, not both, checked at validation; the grant vocabulary is
   Ledger's (the `ledger_grants` data source) and is not copied into the provider, so Ledger refuses an unknown grant or one the root
   token does not hold. `root` defaults to `false`; a root token's own grants are empty and read back as null.
6. **Expiry.** `expires_at` (RFC 3339) is kept as written. Ledger may silently cap it at the creating token's expiry, so the value that
   applies is the computed `effective_expires_at`; this avoids an inconsistent-result error for a capped token. A token that expires stays in
   state with status `expired`: rotate it by changing an argument or by `terraform apply -replace`. Each refresh warns that the token has expired, so the dead secret is not silent.
7. **Drift.** Read uses `GET /tokens/{id}` (`tokens:read`). A token that is gone or revoked outside Terraform is removed from state, so the
   next apply creates a new one (the old secret no longer works).
8. **Destroy revokes** (`DELETE /tokens/{id}`); a token that is already gone is a success. The hints for `forbidden` on create and
   `root_token_required` on revoke name the root-token prerequisite; a 403 on a read (the resource's refresh and both data sources) names the `tokens:read` grant.
9. **No import.** An imported token would have no secret, so import is not offered; the data sources read tokens that exist.
10. **Prerequisite and tests.** The root-token prerequisite is documented on the resource and the data sources. Offline tests run against
    a fake that refuses non-root creation; acceptance tests run against the pinned local Ledger with the bootstrap root token, prove the
    issued secret works, that a replaced or destroyed token gets 401, and that a limited token is refused with the documented hint.

## Why

- A sensitive computed attribute is the only way to hand a generated one-time secret to Terraform; the honest response is to say so
  first in the documentation, rather than to hide it.
- Replacing on every change matches the API: there is nothing to patch, and a token with different grants is a different credential.
- Keeping `expires_at` as the configured value and reporting the applied value separately is the only way to stay consistent when the
  server changes a value that the practitioner wrote.

## Consequences

- The Terraform state is a credential store for these tokens. Anyone who can read it can act as the token until it expires or is revoked.
- A token revoked elsewhere is replaced silently on the next apply (with a new secret); consumers of the old secret must pick up the new one.
- Revoking a parent token also revokes the tokens created through it, including ones this configuration did not create.
- `ledger_api_token` cannot adopt an existing token.
