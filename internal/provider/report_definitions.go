package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/datasource"
)

// The read-only report data sources of ADR-0012 (coverage inventory item F5). They read; they never write, and they
// never combine amounts of different currencies: every amount carries its own currency.

const (
	derivedNote = "These are derived reports, not configuration. "
	bankNote    = "Balances and bank transaction details are bank-sourced facts from the connected provider; they are kept apart from the person's own corrections (manual payments, status overrides, match decisions) and are not edited here. They are copied into the Terraform state, so protect the state accordingly. "
)

func sIn(name, param, desc string) input { return input{name: name, param: param, desc: desc} }
func idIn(name, param, desc string) input {
	return input{name: name, param: param, desc: desc, isID: true}
}
func monthIn(name, param string, required bool, desc string) input {
	return input{name: name, param: param, required: required, desc: desc, pattern: monthRegexp, patDesc: "must be a month as YYYY-MM"}
}

func matchActor(d string) node {
	return obj(d,
		f("kind", rstr("user, token or system.")),
		f("user_id", rstr("The user, when there is one.")),
		f("via", rstr("session, token, mcp or system.")))
}

func occurrenceFields() []field {
	return []field{
		f("id", rstr("The occurrence identifier. Ledger derives occurrences from commitments and may recreate them.")),
		f("commitment_id", rstr("The commitment it belongs to.")),
		f("month", rstr("The month it falls in, YYYY-MM.")),
		f("due_date", rstr("The due date, YYYY-MM-DD.")),
		f("amount", money("The amount due.")),
		f("version", integer("The optimistic-concurrency version.")),
		f("created_at", rstr("When it was created (RFC 3339, UTC).")),
		f("updated_at", rstr("When it last changed (RFC 3339, UTC).")),
		f("cancelled_at", rstr("When it was cancelled, or null.")),
		f("cancelled_reason", rstr("out_of_range, terms_change or commitment_deleted, or null.")),
		f("payment", obj("The payment state. status is what Ledger shows: it is derived_status from the evidence unless the person set an override; the override and its reason are kept separately so the two are never confused.",
			f("status", rstr("paid, partially_paid, upcoming, due, overdue, not_recorded or cancelled.")),
			f("derived_status", rstr("The status derived from recorded evidence alone, ignoring any override.")),
			f("timing", rstr("upcoming, due, overdue or not_recorded, or null.")),
			f("paid_amount", money("The amount paid, in the currency of the occurrence. Evidence in another currency is not converted here: see unconverted_currencies.")),
			f("outstanding_amount", money("What is still owed.")),
			f("overpaid_amount", money("What was paid beyond the amount due.")),
			f("shortfall_amount", money("What is missing against a tolerance-accepted payment.")),
			f("unconverted_currencies", list("Currencies of payment evidence that could not be counted because no conversion applies.", rstr(""))),
			f("evidence_count", integer("Payments counted as evidence, bank and manual.")),
			f("manual_evidence_count", integer("Of those, the ones the person recorded by hand.")),
			f("bank_evidence_count", integer("Of those, the ones matched to a bank transaction.")),
			f("grace_until", rstr("The end of the creditor's grace period, or null.")),
			f("within_grace", rbool("True while the occurrence is late but inside the grace period.")),
			f("leeway", rstr("not_recorded, unknown, none, period or deadline, or null.")),
			f("override", obj("The person's status override, or null when there is none.",
				f("status", rstr("paid or unpaid.")),
				f("reason", rstr("Why the person set it.")),
				f("set_at", rstr("When it was set.")),
				f("applied", rbool("Whether it currently decides the status.")),
				f("previous_status", rstr("The status it replaced, or null.")))))),
	}
}

func without(fs []field, name string) []field {
	var out []field
	for _, fl := range fs {
		if fl.name != name {
			out = append(out, fl)
		}
	}
	return out
}

func summaryItem() node {
	return obj("An occurrence with its commitment's name and any deferral plan.",
		append(occurrenceFields(),
			f("commitment_name", rstr("The commitment's name.")),
			f("deferral", rawJSON("The deferral plan of the occurrence as the API's JSON, or null.")))...)
}

var reports = []report{
	{
		typeName: "me", path: "/me",
		desc: "Who the provider acts as: the owner and role of the API token and the grants it holds.",
		out: []field{
			f("kind", rstr("user or token.")), f("user_id", rstr("The owner's identifier.")),
			f("token_id", rstr("The token's identifier, or null for a session.")), f("role", rstr("administrator or read-only.")),
			f("grants", list("The grants the token holds, such as commitments:read.", rstr(""))),
		},
	},
	{
		typeName: "grants", path: "/grants",
		desc: "The grant vocabulary of Ledger: the resources and actions a token can be granted, and the grants each role has. Useful when declaring token grants.",
		out: []field{
			f("resources", list("The resources.", obj("",
				f("name", rstr("The resource name, as in name:read.")), f("description", rstr("What it covers.")),
				f("actions", list("read and or write.", rstr(""))), f("admin_only", rbool("True when only administrators can hold it."))))),
			f("roles", list("The roles.", obj("", f("name", rstr("administrator or read-only.")), f("grants", list("The grants of the role.", rstr("")))))),
		},
	},
	{
		typeName: "auth_methods", path: "/auth/methods",
		desc: "The sign-in methods the server offers. This is public information; the provider itself always authenticates with an API token.",
		out: []field{
			f("password", obj("Password sign-in.", f("enabled", rbool("Whether it is enabled.")))),
			f("oidc", obj("Single sign-on.", f("enabled", rbool("Whether it is enabled.")), f("name", rstr("The provider's display name, or null.")), f("authorize_url", rstr("The sign-in URL, or null.")))),
		},
	},
	{
		typeName: "merchant_sources", path: "/merchants/sources",
		desc: "The sources Ledger can use to look merchants up, with what each one sends off the machine, so an operator can see what is enabled.",
		out: []field{f("sources", list("The sources.", obj("",
			f("name", rstr("The source name.")), f("kind", rstr("local or remote.")), f("enabled", rbool("Whether it is enabled.")),
			f("description", rstr("What it does.")), f("sends_off_machine", list("What leaves the machine when it is used.", rstr(""))),
			f("attribution", rstr("Attribution text the source requires, or null.")), f("stores_logo_ref", rbool("Whether a logo reference is stored.")),
			f("retention_hours", integer("How long results are kept, or null.")))))},
	},
	{
		typeName: "rates_status", path: "/rates/status",
		desc: "The state of the locally cached currency-conversion rates: the provider, how fresh they are and the last refresh. Rates are provider-sourced data; this shows their provenance and freshness.",
		out: []field{
			f("enabled", rbool("Whether rate refreshing is enabled.")), fk("rate_provider", "provider", rstr("The rate provider.")), f("source", rstr("The source the rates come from.")),
			f("reporting_currency", rstr("The reporting currency, or null.")), f("refresh_available", rbool("Whether a refresh can be started.")),
			f("refresh_interval_seconds", integer("The scheduled refresh interval.")), f("refresh_after_seconds", integer("How long a rate is considered recent.")),
			f("stale_after_days", integer("After this many days without a newer rate date a rate is stale.")),
			f("last_run", obj("The last refresh run, or null.",
				f("trigger", rstr("scheduled or manual.")), f("started_at", rstr("When it started.")), f("finished_at", rstr("When it finished.")),
				f("outcome", rstr("succeeded, partial or failed.")), f("currencies_refreshed", integer("How many currencies were refreshed.")),
				f("error", obj("The error, or null.", f("kind", rstr("unavailable, rate_limited, permanent or internal.")), f("code", rstr("The error code.")))))),
			f("last_success_at", rstr("When a refresh last succeeded, or null.")), f("as_of", rstr("When this status was read.")),
		},
	},
	{
		typeName: "exchange_rates", path: "/rates", paged: true,
		desc: "Locally cached currency-conversion rates with their provider, source, dates and staleness. Ledger never converts silently: a stale or missing rate is shown, not hidden.",
		inputs: []input{
			sIn("rate_provider", "provider", "Only rates of this provider."), sIn("source", "source", "Only rates from this source."),
			sIn("base", "base", "Only rates with this base currency."), sIn("quote", "quote", "Only rates with this quote currency."),
			sIn("date_gte", "date[gte]", "Only rates dated on or after this day (YYYY-MM-DD)."), sIn("date_lte", "date[lte]", "Only rates dated on or before this day (YYYY-MM-DD)."),
		},
		items: obj("A cached rate.",
			fk("rate_provider", "provider", rstr("The rate provider.")), f("source", rstr("The source.")), f("base", rstr("Base currency.")), f("quote", rstr("Quote currency.")),
			f("value", rstr("The rate: how many units of quote one unit of base buys, an exact decimal string.")), f("date", rstr("The date the rate is for.")),
			f("retrieved_at", rstr("When it was last retrieved.")), f("first_retrieved_at", rstr("When it was first retrieved.")),
			f("confirmed_through", rstr("The latest date the provider confirmed it for.")), f("stale", rbool("Whether it is stale.")),
			f("stale_reason", rstr("not_retrieved_recently or rate_date_too_old, or null.")),
			f("previous", obj("The value it was revised from, or null.", f("value", rstr("The earlier value.")), f("revised_at", rstr("When it was revised."))))),
	},
	{
		typeName: "occurrences", path: "/occurrences", paged: true,
		desc: derivedNote + "The occurrences of commitments in one month with their payment state.",
		inputs: []input{
			monthIn("month", "month", true, "The month, YYYY-MM."), idIn("commitment_id", "commitment_id", "Only occurrences of this commitment."),
			idIn("category_id", "category_id", "Only occurrences in this category."), idIn("label_id", "label_id", "Only occurrences with this label."),
			{name: "include_cancelled", param: "include_cancelled", kind: inBool, desc: "Include cancelled occurrences."},
		},
		items: obj("An occurrence.", occurrenceFields()...),
	},
	{
		typeName: "monthly_summary", path: "/occurrences/summary",
		desc: derivedNote + "The month's commitments: paid, upcoming and overdue, with totals per currency (never added across currencies) and how fresh the bank data behind them is. It is never a guarantee of money that is safe to spend.",
		inputs: []input{monthIn("month", "month", true, "The month, YYYY-MM."),
			idIn("category_id", "category_id", "Only this category."), idIn("label_id", "label_id", "Only this label.")},
		out: []field{
			f("as_of", rstr("The date the summary was computed for.")),
			f("filter", obj("The applied filter, or null.", f("category_ids", list("Category identifiers.", rstr(""))), f("label_ids", list("Label identifiers.", rstr(""))), f("note", rstr("What the filter means.")))),
			f("items", list("The month's occurrences.", summaryItem())),
			f("carried_in", list("Overdue occurrences carried in from earlier months by a deferral plan.", summaryItem())),
			f("totals", list("One entry per currency.", obj("",
				f("currency", rstr("The currency of every amount in this entry.")), f("due", money("Total due.")), f("paid", money("Total paid.")),
				f("outstanding", money("Total outstanding.")), f("overdue", money("Total overdue.")), f("overpaid", money("Total overpaid.")),
				f("occurrence_count", integer("Occurrences counted.")), f("overdue_count", integer("Overdue occurrences.")),
				f("unconverted_evidence_count", integer("Payment evidence that could not be counted for lack of a conversion."))))),
			f("freshness", obj("How current the numbers are.",
				f("computed_at", rstr("When the summary was computed.")), f("occurrences_changed_at", rstr("When the occurrences last changed, or null.")),
				f("bank", obj("The bank data behind payment matching.",
					f("available", rbool("Whether bank data is available.")), f("connections", integer("Connections counted.")),
					f("state", rstr("none, unknown, fresh, stale, never_refreshed, failed, disconnected or reconnect_required.")),
					f("last_success_at", rstr("The last successful refresh, or null.")), f("note", rstr("A human-readable note.")))))),
		},
	},
	{
		typeName: "outlook", path: "/outlook",
		desc: derivedNote + bankNote + "Compares the recorded commitments still due with the bank balances, per currency. **The outlook is never a guarantee of money that is safe to spend**: it only knows commitments that were recorded and balances as fresh as the last refresh. Amounts of different currencies are never added; the optional converted block exists only when the server was asked for a currency and has the rates.",
		inputs: []input{monthIn("through", "through", false, "Include commitments through this month, YYYY-MM."),
			sIn("currency", "currency", "Also convert into this currency (needs cached rates)."), idIn("bucket", "bucket", "Only the commitments and accounts of this bucket.")},
		out: []field{
			f("as_of", rstr("The date it was computed for.")), f("notice", rstr("The server's own caution about reading the outlook.")),
			f("slice", rawJSON("The bucket slice as the API's JSON, or null.")),
			f("window", obj("The months considered.", f("not_recorded_count", integer("Commitments with no recorded payment state.")), f("from", rstr("First month, or null.")), f("through", rstr("Last month.")), f("truncated", rbool("Whether the window was cut short.")))),
			f("currencies", list("One entry per currency.", obj("",
				f("currency", rstr("The currency of every amount in this entry.")), f("still_due", money("Recorded commitments still due.")), f("overdue", money("Overdue part of that.")), f("upcoming", money("Upcoming part of that.")),
				f("planned_outflows", obj("Planned outflows from deferral plans.", f("total", money("Total.")), f("deferred_in", money("Deferred into the window.")), f("deferred_beyond", money("Deferred beyond it.")), f("adjustments", money("Adjustments.")), f("missed_count", integer("Missed plans.")), f("missed_outstanding", money("Outstanding on missed plans.")))),
				f("overdue_count", integer("Overdue occurrences.")), f("unconverted_evidence_count", integer("Evidence not counted for lack of a conversion.")),
				f("balance", money("The summed bank balance in this currency, or null when none is counted.")),
				f("balance_less_still_due", money("balance minus still_due, or null. Not money that is safe to spend.")),
				f("account_count", integer("Accounts counted.")), f("stale_account_count", integer("Of those, accounts whose data is stale.")),
				f("oldest_balance_obtained_at", rstr("When the oldest counted balance was obtained, or null.")),
				f("overdue_months", list("Overdue amounts per month.", obj("", f("month", rstr("YYYY-MM.")), f("count", integer("Occurrences.")), f("amount", money("Amount."))))),
				f("balance_as_of", rstr("The reference date of the balance, or null.")),
				f("payments_after_balance", obj("Payments recorded after the balance date, or null.", f("count", integer("Payments.")), f("amount", money("Amount."))))))),
			f("balances", obj("Whether balances are shown.", f("shown", rbool("Whether balances are included.")), f("note", rstr("Why or why not.")), f("stale_after_seconds", integer("After this age a balance is stale, or null.")))),
			f("accounts", list("The bank accounts behind the balances.", obj("",
				f("id", rstr("The account identifier.")), f("connection_id", rstr("The bank connection.")), f("name", rstr("The account name (bank-sourced).")), f("institution", rstr("The institution (bank-sourced).")), f("currency", rstr("The account currency.")),
				f("balances", list("The balances the bank reported.", obj("", f("type", rstr("booked, available, expected or other.")), f("provider_type", rstr("The provider's own type.")), f("amount", money("The balance; may be negative.")), f("reference_date", rstr("The reference date, or null.")), f("obtained_at", rstr("When it was obtained."))))),
				f("counted", rbool("Whether it is counted in the currency totals.")), f("not_counted_reason", rstr("no_balance, no_longer_reported or unsupported_balance_type, or null.")),
				f("freshness", obj("How current the account's data is.", f("state", rstr("fresh, stale, failed, reconnect_required, disconnected or never_refreshed.")), f("stale", rbool("Whether it is stale.")),
					f("last_success_at", rstr("Last success, or null.")), f("last_attempt_at", rstr("Last attempt, or null.")), f("last_error_kind", rstr("Kind of the last error, or null.")), f("last_error_code", rstr("Code of the last error, or null.")), f("notice_code", rstr("A notice code, or null."))))))),
			f("converted", obj("The outlook converted into the requested currency, or null. Present only with the currency filter.",
				f("currency", rstr("The target currency.")), f("complete", rbool("False when some currencies could not be converted.")),
				f("still_due", money("Converted still due.")), f("overdue", money("Converted overdue.")), f("planned_total", money("Converted planned outflows.")),
				f("balance", money("Converted balance, or null.")), f("balance_less_still_due", money("balance minus still_due, or null.")),
				f("balance_account_count", integer("Accounts counted.")), f("stale_balance_account_count", integer("Stale accounts among them.")),
				f("included", rawJSON("Currencies converted, with the rate used and its age, as the API's JSON list.")),
				f("excluded", rawJSON("Currencies left out because the rate is stale or missing, as the API's JSON list.")))),
			f("planned_outflows", obj("Whether planned outflows are included.", f("included", rbool("Included.")), f("note", rstr("A note.")))),
			f("freshness", obj("How current it is.", f("computed_at", rstr("When computed.")), f("occurrences_changed_at", rstr("When the occurrences last changed, or null.")))),
		},
	},
	{
		typeName: "payment_matches", path: "/payment-matches", paged: true,
		desc: derivedNote + bankNote + "Payment matches: the link between an occurrence and a bank transaction, with who suggested and decided it. A suggestion is not proof of payment; only confirmed matches count as evidence.",
		inputs: []input{idIn("occurrence_id", "occurrence_id", "Only matches of this occurrence."), idIn("transaction_id", "transaction_id", "Only matches of this bank transaction."),
			idIn("category_id", "category_id", "Only this category."), idIn("label_id", "label_id", "Only this label."),
			sIn("state", "state", "suggested, confirmed, rejected or reversed."), sIn("origin", "origin", "auto_rule, model_suggestion or user.")},
		items: paymentMatchNode(),
	},
	{
		typeName: "payment_match", path: "/payment-matches/{id}",
		desc:   derivedNote + bankNote + "One payment match.",
		inputs: []input{{name: "id", required: true, isID: true, desc: "The payment match identifier (a ULID)."}},
		out:    without(paymentMatchNode().fields, "id"),
	},
	{
		typeName: "audit_entries", path: "/audit", paged: true,
		desc: "The audit log, oldest first (in identifier order), as the API lists it: with the default max_items on a busy log the result may be truncated, so use at_gte to reach recent entries. Needs a token with the audit grant. Details are the API's own JSON and never contain secrets.",
		inputs: []input{
			{name: "action", param: "action", kind: inStrList, desc: "Only these actions."}, sIn("action_prefix", "action[prefix]", "Only actions with this prefix."),
			{name: "actor_kind", param: "actor_kind", kind: inStrList, desc: "Only these actor kinds: user, token, system or anonymous."},
			idIn("actor_user_id", "actor_user_id", "Only this user's actions."), idIn("actor_token_id", "actor_token_id", "Only this token's actions."),
			sIn("target_type", "target_type", "Only this target type."), sIn("target_id", "target_id", "Only this target."),
			sIn("at_gte", "at[gte]", "Only entries at or after this time (RFC 3339)."), sIn("at_lt", "at[lt]", "Only entries before this time (RFC 3339)."),
		},
		items: obj("An audit entry.",
			f("id", rstr("The entry identifier.")), f("action", rstr("What was done.")), f("actor_kind", rstr("user, token, system or anonymous.")),
			f("actor_user_id", rstr("The user, or null.")), f("actor_token_id", rstr("The token, or null.")), f("target_type", rstr("The kind of object acted on, or null.")),
			f("target_id", rstr("The object acted on, or null.")), f("at", rstr("When it happened (RFC 3339, UTC).")), f("details", rawJSON("Details as the API's JSON object."))),
	},
}

func paymentMatchNode() node {
	return obj("A payment match.",
		f("id", rstr("The match identifier.")), f("occurrence_id", rstr("The occurrence it pays.")), f("transaction_id", rstr("The bank transaction.")),
		f("amount", money("The amount of the transaction assigned to the occurrence.")),
		f("origin", rstr("auto_rule, model_suggestion or user.")), f("state", rstr("suggested, confirmed, rejected or reversed.")),
		f("counts_as_evidence", rbool("Whether the match counts as payment evidence.")), f("needs_review", rbool("Whether a person should review it.")),
		f("bank_text_redacted", rbool("Whether the bank's description text was redacted.")),
		f("created_by", matchActor("Who created it.")), f("decided_by", matchActor("Who decided it, or null.")), f("decided_at", rstr("When, or null.")), f("reason", rstr("The reason given, or null.")),
		f("transaction", obj("The bank transaction (a bank-sourced fact).",
			f("source", rstr("Always bank.")), f("id", rstr("The transaction identifier.")), f("account_id", rstr("The account.")), f("provider", rstr("The banking provider.")),
			f("booking_status", rstr("booked, pending, cancelled or other.")), f("amount", money("The amount; may be negative.")), f("date", rstr("The transaction date.")),
			f("booking_date", rstr("Or null.")), f("value_date", rstr("Or null.")), f("description", rstr("The bank's text, or null when redacted.")),
			f("counterparty", rstr("The counterparty, or null.")), f("no_longer_reported_at", rstr("When the bank stopped reporting it, or null.")),
			f("superseded_by", rstr("A transaction that replaced it, or null.")), f("countable", rbool("Whether it can count as evidence.")))),
		f("history", list("What happened to the match.", obj("",
			f("action", rstr("suggested, confirmed, rejected, unmatched, corrected, reversed or unconfirmed.")), f("state", rstr("The state after it.")), f("amount", money("The amount then.")),
			f("by", matchActor("Who.")), f("reason", rstr("The reason, or null.")), f("at", rstr("When.")),
			f("replaced_manual_payment_ids", list("Manual payments it replaced.", rstr(""))), f("restored_manual_payment_ids", list("Manual payments it restored.", rstr("")))))),
		f("version", integer("The version.")), f("created_at", rstr("When it was created.")), f("updated_at", rstr("When it last changed.")))
}

// reportDataSources are the constructors registered by the provider.
func reportDataSources() []func() datasource.DataSource {
	out := make([]func() datasource.DataSource, 0, len(reports))
	for _, r := range reports {
		out = append(out, newReport(r))
	}
	return out
}
