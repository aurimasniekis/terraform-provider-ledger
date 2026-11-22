# ADR-0011: Manual payments and status overrides

Status: Accepted (2026-11-22)

## Context

Task F4 of the coverage inventory (ADR-0005) adds the two user corrections Ledger keeps on an occurrence: a payment the person
records by hand (go-ledger ADR-0031) and the manual status override (decision D7). Both are **user data kept apart from bank
facts**. An occurrence itself is derived by Ledger from a commitment and is never managed here.

## Decision

1. **Objects.** Resources `ledger_manual_payment` and `ledger_occurrence_status_override`; data sources `ledger_manual_payment`
   (by occurrence and payment identifier), `ledger_manual_payments` (every page of one occurrence's records) and
   `ledger_occurrence_status_override` (whether an override is active, and what it is).
2. **Bank facts stay out.** Neither resource nor data source reads, writes or lists a bank transaction, a match or a balance. The only
   bank-related value is `replaced_by_match_id` on a payment, a read-only identifier Ledger sets when the person confirms a bank
   payment as the payment they recorded; the record then does not count and Terraform shows no difference. The override
   resource also reads back `derived_status` (what evidence and dates say before the override) and `payment_status`, both computed
   and read-only, as of the last refresh.
3. **Exact money.** `amount` is an exact decimal string validated at plan time (greater than zero) with its own `currency`, sent
   and read back as written, scale included. No number type is involved. The currency must be the occurrence's: Ledger refuses a
   payment in another currency and never converts, and the provider surfaces that refusal. `paid_on` is checked as a real
   date at plan time; "not after today in the reporting zone" is Ledger's rule, reported by it, because the provider does not
   know the zone.
4. **A manual payment cannot be changed.** Ledger does not edit records ("delete a mistaken one and record it again"), so every
   attribute replaces. Destroy deletes the record against the version last read (a stale version fails with a message to
   refresh). Ledger soft-deletes it and derives the occurrence status again. A 404 on read removes it from state. Import is
   `<occurrence_id>/<payment_id>`.
5. **The override is one resource per occurrence.** Its identifier is the occurrence's. Create refuses an occurrence that already has
   an active override (import it instead): Terraform does not replace an override it did not make, so the history Ledger keeps
   holds only steps Terraform made. A change of `status` or `reason` is a PUT in place. **Destroy undoes the history step by step until
   no override is active**, because Ledger's undo restores the override a change replaced. A `paid` override needs payment
   evidence (409 `payment_evidence_required`, surfaced). A read that finds no active override removes the resource from state.
6. **Occurrence version and concurrency.** An override is written against the occurrence's version, which moves with every payment-state
   change, including those made by other resources in the same run. The provider therefore reads the version immediately before each
   write and retries a conflict up to five times with a fresh read; the content of the override does not depend on the version. A
   conflict that persists is reported with a message to refresh (D17). **The version is not the guard against out-of-band override
   changes; the override's content is.** Every attempt of Create, Update and the first undo of Delete re-reads the occurrence and
   proceeds only while the active override is what Terraform last read (none for Create; status, reason and `set_at` otherwise).
   Otherwise it fails with the refresh message, so an override changed in the UI between plan and apply is neither overwritten nor
   undone. Later undo steps of a destroy walk Ledger's history unguarded. Ordering an override after its payment with `depends_on` also keeps
   the read-back `derived_status` current at apply time.
7. **Finding the occurrence.** The occurrence identifier is an input. Ledger derives occurrences, so it is found with the
   `ledger_occurrence` data source (ADR-0010: by identifier, or by commitment and reporting month); descriptions and examples use it.

## Limitations (open, not silent)

- Verified against a local Ledger built from the pin only (API 1.0.0). The retry of a version conflict, the retry bound, the
  destroy walk and the out-of-band guard are covered by deterministic fake-server tests (`status_override_unit_test.go`) and by acceptance.
- A destroy of an override walks Ledger's undo history, so it also removes steps made before the person imported the resource, and there is no redo through the provider. The resource documentation says so and points to `terraform state rm` to stop managing without undoing.
- `applied`, `derived_status` and `payment_status` can be stale between refreshes; they never cause a plan difference.
