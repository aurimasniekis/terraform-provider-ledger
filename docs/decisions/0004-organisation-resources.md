# ADR-0004: Organisation resources and data sources (categories, labels, buckets, assignments)

Status: Accepted (2026-11-22)

## Context

Task T3 covers go-ledger's organisation model (decision D12, go-ledger ADR-0053): categories (single-valued, two levels),
labels (many-valued, flat), buckets (groupings of commitments and accounts that never hold money), archive-first
lifecycle, assignments with an origin (user, rule or model) where the person's word outranks automatic ones. The
Management API for it is served by go-ledger's `work` branch.

## Decision

1. **Resources.** `ledger_category`, `ledger_label`, `ledger_bucket` (the definitions) and the person's own assignments:
   `ledger_category_assignment` (one per object; omitting `category_id` records "the person chose none"),
   `ledger_label_assignment` (one per label per object) and `ledger_bucket_member`. **Data sources:** `ledger_category`,
   `ledger_label`, `ledger_bucket` (by id or by name), `ledger_categories`, `ledger_labels`, `ledger_buckets` (filtered lists),
   `ledger_bucket_members` and `ledger_object_organisation` (everything assigned to an object, whatever the origin).
2. **Archive first.** Definitions have `archived` (default `false`), applied through the archive and unarchive routes
   (history stays). Destroy deletes; when the API refuses with `definition_in_use` the error tells the person to archive or
   to set `reassign_to`. `reassign_to` is a destroy-time argument kept in state and never read back (so it is empty after
   an import); the API moves the assignments and records them as the person's.
3. **Optimistic concurrency.** Changes and deletes send the version Terraform last read as `If-Match`. A stale version is a
   failure with a message to refresh, never an overwrite (D17). The provider does not retry on conflict.
4. **Drift.** Read maps 404 to "removed". An assignment resource owns only the person's own, direct decision: if the
   category or label is gone, or is now an automatic (rule or model) assignment, or is only inherited from a pending
   transaction, Read removes it from state so the plan shows it as to be created. Out-of-band renames, archive state
   and parent changes show as updates.
5. **Names.** Ledger trims and collapses white space. A configured name that differs after that would never match what is
   read back, so a validator rejects it at plan time instead of producing a permanent difference. Data-source lookups by
   name ignore case and return the name as written (Terraform requires a configured value to come back unchanged).
6. **Destroying assignments has API semantics, documented on the resources:** destroying a category assignment resets the
   decision (rules and models may assign again); destroying a label assignment records that the person removed the
   label (rules and models do not add it back).
7. **Identifiers** are validated as ULIDs before they reach a URL path, so a value can never alter the path.
8. **Client.** Hand-written methods in `internal/client` (the OpenAPI document is the contract, as in go-ledger ADR-0011);
   error bodies that are not the JSON envelope are never echoed.
9. **Pin.** `internal/ledgertest/ledger.pin` moves from `fad378e` to go-ledger commit `760bba6` (still API 1.0.0), the first
   one on `work` that serves the organisation routes. This supersedes the commit named in ADR-0002 item 1.

## Limitations (open, not silent)

- **Merchants are not supported** as assignment targets. In the pinned API merchants have no record: they are addressed by
  a provisional name key (go-ledger ADR-0053 item 7, PROVISIONAL), and every merchant route needs an administrator-only
  grant. Encoding that key in Terraform state would bake in a contract that is about to be replaced by merchant record
  identifiers. Merchant assignment is **deferred until the merchant-record change lands on go-ledger `work`**; the
  resources then accept `merchant` with the record identifier. This is a deferral of agreed scope and is reported to
  control, which owns the scope decision.
- Rules and models assign through Ledger itself; Terraform only manages the person's own assignments and reads the rest.
- No retry of reads: D17 allows idempotent-read retries (at most 3, with backoff); the shared client does not implement them
  yet and the task did not add them.
- The acceptance job needs a Terraform CLI. `terraform-plugin-testing` uses `TF_ACC_TERRAFORM_PATH` or a `terraform` on PATH
  and otherwise downloads one, which is a network call at test time; the CI acceptance job has not been changed or run
  here.
- Verified against a local Ledger built from the pin only; the version-conflict path through Terraform (an update racing
  an out-of-band change) is covered by the client unit tests and the fake server, not by the acceptance run.
