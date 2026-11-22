# ADR-0014: Users and write-only passwords

Status: Accepted (2026-11-22)

## Context

Task F1 of the coverage inventory (ADR-0005) adds Ledger's users (`/users`) and the password operation (`PUT /users/{id}/password`).
Ledger has **no user delete**: a user is disabled, which keeps audit history and token ownership. With an API token, creating or
promoting an administrator, re-enabling a disabled administrator, changing the root (first) account and setting any password need
the **root token** of the root account (decision D32 of go-ledger); any other token is refused with 403 `root_token_required`.
Secrets are write-only in this provider (ADR-0005).

## Decision

1. **Objects.** Resources `ledger_user` and `ledger_user_password`; data sources `ledger_user` (by `id` or exact `username`) and
   `ledger_users` (filters `roles`, `disabled`, `username_prefix`; every page is read).
2. **A user is a resource; destroy is disable.** `username`, `display_name`, `role` and `disabled` change in place with one merge patch of
   only what changed (clearing the display name sends `null`). Destroy reads the user and disables it with the version just read
   (the intent is "disabled" whatever else changed), does nothing when it is gone or already disabled, and removes it from state. The
   disabled user stays in Ledger; creating the same username again is refused by Ledger (taken), so a person re-enables it by
   importing it (`terraform import`) and setting `disabled = false`. Ledger's refusal to leave no enabled administrator (409) is
   shown with a sentence of advice.
3. **Import** by identifier, for users and nothing else. A created user is tracked in state before an optional "disabled" step, so a
   failure there taints it. The taint is kept on purpose: Terraform core rejects saving `disabled = false` for a planned
   `disabled = true`, so state cannot record the user as disabled. The user then exists and is enabled. A plain re-apply would replace
   it, disabling the old user and then failing because the username is taken. Recovery is `terraform untaint` and then apply, which
   disables the user in place, or `terraform state rm` and `terraform import` by identifier, then apply.
4. **The password is write-only.** `ledger_user_password` has `user_id` (replaces on change), `password_wo` (write-only, sensitive,
   required: read from the configuration during create and update, never in plan or state; Terraform 1.11 or later) and
   `password_wo_version` (stored; raising it is how a new password is applied, because Terraform cannot see a write-only value change).
   `sessions_ended` reports what the last change ended. Reading only checks the user still exists: a password changed elsewhere cannot
   be seen. Destroy only forgets (Ledger cannot remove a password); disabling the user stops sign-in. No import (nothing to read back).
5. **Nothing secret in output.** The password is passed as a `client.Secret`, only revealed when put on the wire; no error or
   diagnostic formats it, and unit tests check the state for it.
6. **Root token hint.** A `root_token_required` refusal is shown with the advice to run the provider with the root token for that
   change. Management of read-only users, display names and disabling other users works with a `users:write` token.
7. **No hidden defaults.** `disabled` defaults to `false`; the role has no default. The role set is the two the API serves
   (`administrator`, `read-only`).

## Why

- Disable-on-destroy is the only deletion Ledger offers and is documented on the resource, rather than hiding a missing delete.
- A version attribute for the write-only value follows the framework's pattern: a rotation must be an explicit, planned change.
- Separating the password from the user keeps the user manageable with a lesser token and the root-token requirement local to the
  password.

## Consequences

- A password in the Terraform configuration is read at apply time, so it should come from an ephemeral source.
- `ledger_user_password` cannot detect a changed password and does not undo one on destroy.
