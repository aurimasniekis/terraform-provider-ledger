# ADR-0010: Deferral plans, adjustments, history and occurrence lookup

Status: Accepted (2026-11-22)

## Context

Task F3 of the coverage inventory (ADR-0005) adds deferral plans (go-ledger ADR-0050) to the provider. A plan is the person's own
record that an outstanding occurrence will be paid on a later date. It is not payment, forgiveness or a creditor agreement, and it
never changes the occurrence's due date, amount, payment evidence or status. A plan hangs off an **occurrence**, which Ledger derives
from a commitment's schedule and may rewrite in place, cancel or recreate. Fees and interest are explicit adjustments of a plan, with a
paid marker that is the person's own statement and never payment evidence.

## Decision

1. **Objects.** Resources `ledger_deferral_plan` and `ledger_deferral_plan_adjustment`; data sources `ledger_deferral_plan` (by
   identifier), `ledger_deferral_plans` (filters `occurrence_id`, `commitment_id`, `state`, `planned_month`; every page is read),
   `ledger_deferral_plan_history` (every page of a plan's append-only history) and `ledger_occurrence`.
2. **Finding the occurrence.** `ledger_occurrence` looks one occurrence up by `id`, or by `commitment_id` and `month` (a commitment has at
   most one occurrence in a month; `include_cancelled` is opt-in). It never creates anything: reading a month makes Ledger materialise
   the commitment's occurrences. A month with none (a quarterly schedule skips months, outside the start and end dates, cancelled) fails
   with an error that says so. It exposes the derived payment state (status, timing, paid and outstanding amounts, unconverted
   currencies, grace) as read-only values, derived on every read. It is the by-identifier lookup F5 listed for `GET /occurrences/{id}`
   as far as a plan needs it; the month listing, monthly summary and outlook stay with F5 (`ledger_occurrences`).
3. **A plan is a resource; destroy is undo.** `occurrence_id` requires replacement (the old plan is undone, a new one made); `plan_type`,
   `planned_date` and `reason` revise in place (a merge patch of all three, `null` clearing the reason). Destroy calls the undo
   operation: Ledger keeps the undone plan and its history. A plan found `undone` or missing on read leaves state with a warning, so the next
   plan creates it again; Ledger lets the occurrence be deferred anew.
4. **Only stable members are attributes of the resource.** The plan's version and `updated_at` move with every adjustment of the plan, and
   the occurrence's status, the outstanding amount, `missed`, grace and the adjustment totals change without any change to the plan. As
   resource attributes they would show as drift or go stale inside one apply (the acceptance run showed it for the version). The resource
   keeps what the person set plus the history Ledger fixed when the plan was made (`original_due_date`, `original_amount`,
   `outstanding_when_planned`, `currency`); the data source has the live state and the version.
5. **Exact money.** Every amount is a decimal string with the scale written, in the occurrence's currency; `currency` is explicit on an
   adjustment and must equal the plan's (Ledger refuses otherwise and nothing is converted). No number type is involved.
6. **Adjustments.** A recorded charge is a resource of its own: `kind`, `amount`, `currency` and `note` replace on change (the old
   adjustment is removed and kept by Ledger as history), and `paid` (default `false`) toggles in place by calling Ledger's paid and unpaid
   operations. `paid` is the person's own marker and is documented as not payment evidence for the occurrence. Destroy removes the
   adjustment, paid or not. Import by `<plan id>/<adjustment id>`. A removed adjustment, or one on an undone plan, leaves state on read. When
   creating with `paid = true` fails at the second call, the adjustment stays in state (unpaid) with an error, so Terraform taints it and the next apply replaces it: the first adjustment stays
   in Ledger as removed history and one new one is recorded, so there is never a second active charge (but the history shows an extra
   added and removed pair).
7. **Undo is refused while a fee is planned** (409 `deferral_adjustments_unpaid`). The adjustment resources depend on their plan, so Terraform
   destroys them first and the undo succeeds. Where the refusal still happens (an adjustment recorded outside Terraform), the error says
   to mark it paid or remove it.
8. **Versions and parallel applies.** Every write to a plan, including its adjustments', moves the plan's version, and Terraform applies
   sibling resources of one plan in parallel. Writes by the provider therefore take a per-plan lock, read the plan's current version and send
   it as `If-Match`. A revision additionally compares the plan's current type, date and reason with what Terraform last read and fails with
   the standard "changed outside Terraform" error when they differ, so a person's change is not silently overwritten. A concurrent change by
   another client between the read and the write is still caught by `If-Match`.
9. **Hints.** A 409 with a code a person can act on (`nothing_outstanding`, `occurrence_cancelled`, `deferral_plan_exists`,
   `deferral_plan_undone`, `deferral_adjustments_unpaid`) gets one sentence of advice appended to Ledger's message.

## Why

- Derived state belongs to data sources, as with commitments (ADR-0006): a configuration states what the person decided, and a read tells
  what Ledger derives from it today.
- Marking a fee paid is reversible and not bank evidence, so it is an in-place attribute and not a separate action.
- Reading the current version under a lock, with the type, date and reason test, was chosen over a strict state version because the
  strict form fails an ordinary apply that changes a plan and its fee together.

## Consequences

- Acceptance tests run against the Ledger commit pinned in `internal/ledgertest/ledger.pin` and cover create, revise, import, adjustments (add,
  paid, unpaid, replace), refusals (planned date, second plan, currency), drift (fee removed, plan revised, plan undone) and destroy. The occurrence
  is derived from a commitment starting this month, due next month, so the tests do not depend on the day they run.
- Not covered: a plan on an occurrence that is already overdue or partially paid (the pinned Ledger records a commitment's earlier
  occurrences as "not recorded", which cannot be deferred, and the provider cannot record a payment: manual payments are F4), so `missed`,
  `within_grace` and `overdue_since` are mapped and unit-tested only, not exercised against Ledger.
- Terraform's unit tests use an in-memory stand-in for the deferral endpoints; it enforces If-Match, undo refusal and undone-plan refusal as
  go-ledger ADR-0050 describes them, and is not Ledger.
- An occurrence Ledger recreates gets a new identifier: `ledger_occurrence` returns it, and a plan referring to the old one is replaced.
