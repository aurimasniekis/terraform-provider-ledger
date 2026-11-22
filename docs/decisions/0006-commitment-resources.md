# ADR-0006: Commitment resource and data sources

Status: Accepted (2026-11-22)

## Context

Task T4 adds the commitment definition (go-ledger ADR-0024, D6 recurrence, D9 matching tolerance, D14 leeway) to the
provider. A commitment is user data. Its dated occurrences and payment state are derived by Ledger and are not part of it.

## Decision

1. **Objects.** Resource `ledger_commitment`; data sources `ledger_commitment` (by identifier) and `ledger_commitments`
   (filters `kind`, `priority`, `currency`; every page is read, in identifier order). Import by identifier.
2. **Exact money.** `amount` and `match_tolerance` are strings matching the API's decimal patterns, validated at plan time
   (amount greater than zero, tolerance zero or more) and sent and read back as written, scale included. No number type or
   floating-point value is involved in any step. `currency` is one attribute shared by both amounts, so the provider
   cannot combine currencies; a currency change is an in-place update, and the API refuses it while an occurrence has an
   active deferral plan.
3. **Schedule and leeway are nested objects.** Every schedule form the API has is supported: monthly with an interval (1 to
   120, so quarterly is 3) and annual with a month, each with a day. The validator requires the members of the chosen form
   and rejects the others, so a plan never shows a difference that Ledger would not keep. Leeway uses the four kinds
   `unknown`, `none`, `period` and `deadline`; **an omitted block records nothing, which differs from `unknown` and
   `none`** (D14), and the provider never fills in a default length.
4. **Create and update shapes differ, and the acceptance run found it.** A create is refused (400) when it names a member that
   does not belong to the schedule form; a merge patch needs an explicit null for it to remove what the old form had. The
   client therefore omits those members on create and sends them as null on update, and always sends the whole schedule and
   leeway on update. An update sends the whole writable definition: merging equal values is a no-op, and nothing stale
   can survive.
5. **Optimistic concurrency.** Updates and deletes send the version last read as `If-Match`; a stale version is an error with
   a message to refresh, never an overwrite (D17).
6. **Occurrence effects.** A change to amount, schedule, start or end date reaches Ledger's unpaid occurrences from today
   in the reporting zone (the API default for `effective_from`); paid history and overdue unpaid occurrences keep their
   terms (D6). `effective_from` is deliberately not an attribute: it is not part of the definition, a value kept in state
   would silently apply to later edits, and a future-dated change is a one-off the API still offers.
7. **Destroy.** Ledger deletes the definition and its unpaid occurrences, and refuses (409) while an occurrence has live
   payment evidence. The error tells the person to set `end_date` or to remove the resource from state.
8. **Drift.** A 404 on read removes the resource from state so the plan shows it as to be created; out-of-band changes show as
   updates.

## Limitations (open, not silent)

- The two data sources and the resource are verified against a local Ledger built from the pin only.
- The monthly interval is required (no default), so a configuration states quarterly or monthly explicitly.
- The provider cannot express a future-dated terms change (item 6).
- Whether the API trims or normalises `name` and `notes` is not documented in the OpenAPI text; the acceptance run uses
  names without surrounding white space and shows no difference. A value Ledger changes would show as a permanent diff.
