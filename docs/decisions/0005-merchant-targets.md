# ADR-0005: Merchant targets and merchant records

Status: Accepted (2026-11-22). It resolves the deferral in the limitations of [ADR-0004](0004-organisation-resources.md).

## Context

ADR-0004 left merchants out because the pinned Ledger identified them by a provisional name key. go-ledger ADR-0058 replaced that
with merchant records (a stable identifier, a display name and alias names) with full Management API CRUD, and go-ledger
ADR-0062 organises merchants through `/organisation/merchant/{id}`. Merchant routes need the administrator-only
`merchants:read` / `merchants:write` grants because names derive from bank text.

## Decision

1. **Pin.** `internal/ledgertest/ledger.pin` moves from `760bba6` to go-ledger commit `b595e3b` (the `work` head that carries
   M1 and O5; API version still 1.0.0). This supersedes the commit named in ADR-0004 item 9.
2. **Targets.** `merchant` is an `object_type` of `ledger_category_assignment`, `ledger_label_assignment` and
   `ledger_object_organisation`, addressed by the merchant record identifier (import: `merchant/<id>`). Not of
   `ledger_bucket_member`: bucket members exclude merchants, and the schema rejects it at plan time.
3. **Resources.** `ledger_merchant` (`display_name`, `archived`, computed `merged_into` and the usual version and timestamps;
   import by identifier) and `ledger_merchant_alias` (`merchant_id` + `name`, every attribute forces replacement; import
   `<merchant_id>/<alias_id>`). Data sources: `ledger_merchant` (by id or display name, ignoring case; archived and merged
   merchants included; lists the aliases) and `ledger_merchants` (`include_archived`: the API lists active merchants unless
   `archived=true`, which adds archived and merged ones).
4. **Archive first**, as for definitions. Destroying a merchant that decisions, assignments or merged merchants refer to fails with
   `merchant_in_use` and the error says to archive. Merging merchants is **not** a Terraform resource: it moves aliases, decisions
   and assignments and cannot be undone in place (go-ledger ADR-0058 item 6), so it stays an explicit action in Ledger.
5. **Version bumps by aliases.** Adding or removing an alias bumps the merchant's version. `ledger_merchant_alias` does that in the
   same run as `ledger_merchant`, after the merchant was read, so a stale version on update or delete is retried once with the
   current one, **only when** the display name and archive state still equal what state holds. A change to the managed attributes
   made outside Terraform is still a conflict (ADR-0004 item 3), never an overwrite. The merchant's `version` and `updated_at`
   therefore differ after an import that follows an alias change; acceptance import checks ignore them.
6. **Aliases.** Every merchant has its display name as an alias, and a rename adds the new name as an alias. A `ledger_merchant_alias`
   for a name the merchant already has takes that alias over (destroying the resource removes it); a name another merchant has
   fails with `merchant_alias_taken`. Display names and aliases are validated as 1 to 200 characters with no surrounding or repeated
   white space; Unicode normalisation (NFC) is done by Ledger and not checked at plan time, so write names in NFC.
7. **Grants.** A token without the merchants grants gets Ledger's 403 on any merchant resource, data source or assignment; the
   docs say so. Nothing here widens a token.

## Limitations (open, not silent)

- Merchants are created by hand in Terraform or by Ledger itself (bank-derived merchants appear as Ledger sees transactions); there is
  no resource for confirmations, corrections or enrichment answers.
- Aliases added by Ledger (origin `decision` or `upgrade`) are listed by the data sources but are not managed unless imported.
- An archived merchant refuses new assignments by identifier (`merchant_archived`); existing ones stay and still read.
- Verified against a local Ledger built from the pin only; a token that lacks the merchants grants was not exercised in the
  acceptance run.
