package provider

import (
	"fmt"
	"regexp"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aurimasniekis/terraform-provider-ledger/internal/client"
)

// Patterns of the OpenAPI schemas PositiveDecimal, Money.amount, Currency and Date. Amounts are exact decimal strings:
// they keep their scale and never pass through a floating-point type.
var (
	positiveDecimalRegexp = regexp.MustCompile(`^([1-9][0-9]*(\.[0-9]+)?|0\.[0-9]*[1-9][0-9]*)$`)
	nonNegDecimalRegexp   = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]+)?$`)
	currencyRegexp        = regexp.MustCompile(`^[A-Z]{3}$`)
	dateRegexp            = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
)

const (
	dateLayout = "2006-01-02"
	// minStartDate is the earliest start_date the API accepts.
	minStartDate = "1900-01-01"
)

var (
	commitmentKinds      = []string{"service", "server", "domain", "subscription", "debt_repayment"}
	commitmentPriorities = []string{"essential", "important", "flexible"}
	scheduleFrequencies  = []string{"monthly", "annual"}
	leewayKinds          = []string{"unknown", "none", "period", "deadline"}
)

func positiveDecimalValidator() validator.String {
	return stringvalidator.RegexMatches(positiveDecimalRegexp, "must be an exact decimal greater than zero written as a string, for example \"12.50\"")
}
func nonNegDecimalValidator() validator.String {
	return stringvalidator.RegexMatches(nonNegDecimalRegexp, "must be an exact decimal of zero or more written as a string, for example \"0.50\"")
}
func currencyValidator() validator.String {
	return stringvalidator.RegexMatches(currencyRegexp, "must be an ISO 4217 currency code of three upper-case letters")
}
func dateValidator() validator.String {
	return stringvalidator.RegexMatches(dateRegexp, "must be a calendar date written YYYY-MM-DD")
}
func intBetween(lo, hi int64) validator.Int64 { return int64validator.Between(lo, hi) }

// scheduleModel, leewayModel and commitmentModel are the Terraform view of a commitment. They are shared by the
// resource and both data sources so the mapping to and from the API is written once.
type scheduleModel struct {
	Frequency types.String `tfsdk:"frequency"`
	Interval  types.Int64  `tfsdk:"interval"`
	Month     types.Int64  `tfsdk:"month"`
	Day       types.Int64  `tfsdk:"day"`
}

type leewayModel struct {
	Kind         types.String `tfsdk:"kind"`
	Days         types.Int64  `tfsdk:"days"`
	Until        types.String `tfsdk:"until"`
	Conditions   types.String `tfsdk:"conditions"`
	Consequences types.String `tfsdk:"consequences"`
	Source       types.String `tfsdk:"source"`
}

type commitmentModel struct {
	ID             types.String   `tfsdk:"id"`
	Name           types.String   `tfsdk:"name"`
	Kind           types.String   `tfsdk:"kind"`
	Amount         types.String   `tfsdk:"amount"`
	Currency       types.String   `tfsdk:"currency"`
	Schedule       *scheduleModel `tfsdk:"schedule"`
	StartDate      types.String   `tfsdk:"start_date"`
	EndDate        types.String   `tfsdk:"end_date"`
	Priority       types.String   `tfsdk:"priority"`
	Notes          types.String   `tfsdk:"notes"`
	MatchTolerance types.String   `tfsdk:"match_tolerance"`
	Leeway         *leewayModel   `tfsdk:"leeway"`
	Version        types.Int64    `tfsdk:"version"`
	CreatedAt      types.String   `tfsdk:"created_at"`
	UpdatedAt      types.String   `tfsdk:"updated_at"`
}

func int64OrNull(v *int64) types.Int64 {
	if v == nil {
		return types.Int64Null()
	}
	return types.Int64Value(*v)
}

func nullableInt64(v types.Int64) *int64 {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	n := v.ValueInt64()
	return &n
}

// commitmentFromAPI maps what the API returned to the Terraform model.
func commitmentFromAPI(c client.Commitment) commitmentModel {
	m := commitmentModel{
		ID:        types.StringValue(c.ID),
		Name:      types.StringValue(c.Name),
		Kind:      types.StringValue(c.Kind),
		Amount:    types.StringValue(c.Amount.Amount),
		Currency:  types.StringValue(c.Amount.Currency),
		StartDate: types.StringValue(c.StartDate),
		EndDate:   strOrNull(c.EndDate),
		Priority:  strOrNull(c.Priority),
		Notes:     strOrNull(c.Notes),
		Schedule: &scheduleModel{
			Frequency: types.StringValue(c.Schedule.Frequency),
			Interval:  int64OrNull(c.Schedule.Interval),
			Month:     int64OrNull(c.Schedule.Month),
			Day:       types.Int64Value(c.Schedule.Day),
		},
		MatchTolerance: types.StringNull(),
		Version:        types.Int64Value(c.Version),
		CreatedAt:      types.StringValue(c.CreatedAt),
		UpdatedAt:      types.StringValue(c.UpdatedAt),
	}
	if c.MatchTolerance != nil {
		m.MatchTolerance = types.StringValue(c.MatchTolerance.Amount)
	}
	if l := c.Leeway; l != nil {
		m.Leeway = &leewayModel{
			Kind: types.StringValue(l.Kind), Days: int64OrNull(l.Days), Until: strOrNull(l.Until),
			Conditions: strOrNull(l.Conditions), Consequences: strOrNull(l.Consequences), Source: strOrNull(l.Source),
		}
	}
	return m
}

// input maps the Terraform model to the writable part of a commitment. The match tolerance is in the commitment's
// currency (the API requires the member and the provider never mixes currencies).
func (m commitmentModel) input() client.CommitmentInput {
	cur := m.Currency.ValueString()
	in := client.CommitmentInput{
		Name:      m.Name.ValueString(),
		Kind:      m.Kind.ValueString(),
		Amount:    client.Money{Amount: m.Amount.ValueString(), Currency: cur},
		StartDate: m.StartDate.ValueString(),
		EndDate:   nullableString(m.EndDate),
		Priority:  nullableString(m.Priority),
		Notes:     nullableString(m.Notes),
	}
	if s := m.Schedule; s != nil {
		in.Schedule = client.Schedule{Version: 1, Frequency: s.Frequency.ValueString(), Day: s.Day.ValueInt64()}
		// Only the members of the chosen frequency are kept; the others stay null so a switch replaces the whole form.
		switch in.Schedule.Frequency {
		case "monthly":
			in.Schedule.Interval = nullableInt64(s.Interval)
		case "annual":
			in.Schedule.Month = nullableInt64(s.Month)
		}
	}
	if t := nullableString(m.MatchTolerance); t != nil {
		in.MatchTolerance = &client.Money{Amount: *t, Currency: cur}
	}
	if l := m.Leeway; l != nil {
		in.Leeway = &client.Leeway{
			Kind: l.Kind.ValueString(), Days: nullableInt64(l.Days), Until: nullableString(l.Until),
			Conditions: nullableString(l.Conditions), Consequences: nullableString(l.Consequences), Source: nullableString(l.Source),
		}
	}
	return in
}

func known(v interface {
	IsNull() bool
	IsUnknown() bool
}) bool {
	return !v.IsNull() && !v.IsUnknown()
}

// validateCommitment checks what the schema cannot express: which schedule and leeway members go with which form, and
// real calendar dates. It only judges known values, so an expression resolved at apply time is not rejected early.
func validateCommitment(m commitmentModel) diag.Diagnostics {
	var d diag.Diagnostics
	if s := m.Schedule; s != nil && known(s.Frequency) {
		switch s.Frequency.ValueString() {
		case "monthly":
			if s.Interval.IsNull() {
				d.AddAttributeError(path.Root("schedule").AtName("interval"), "Missing schedule interval",
					"A monthly schedule needs interval: the months between due dates (1 = every month, 3 = quarterly).")
			}
			if known(s.Month) {
				d.AddAttributeError(path.Root("schedule").AtName("month"), "Month does not apply to a monthly schedule",
					"Remove month, or set frequency to annual.")
			}
		case "annual":
			if s.Month.IsNull() {
				d.AddAttributeError(path.Root("schedule").AtName("month"), "Missing schedule month", "An annual schedule needs month (1 to 12).")
			}
			if known(s.Interval) {
				d.AddAttributeError(path.Root("schedule").AtName("interval"), "Interval does not apply to an annual schedule",
					"Remove interval, or set frequency to monthly.")
			}
		}
	}
	if l := m.Leeway; l != nil && known(l.Kind) {
		kind := l.Kind.ValueString()
		p := path.Root("leeway")
		switch {
		case kind == "period" && l.Days.IsNull():
			d.AddAttributeError(p.AtName("days"), "Missing leeway days", "A leeway of kind period needs days (1 to 365).")
		case kind != "period" && known(l.Days):
			d.AddAttributeError(p.AtName("days"), "Days do not apply", "days is only for a leeway of kind period.")
		}
		switch {
		case kind == "deadline" && l.Until.IsNull():
			d.AddAttributeError(p.AtName("until"), "Missing leeway deadline", "A leeway of kind deadline needs until (a date).")
		case kind != "deadline" && known(l.Until):
			d.AddAttributeError(p.AtName("until"), "Deadline does not apply", "until is only for a leeway of kind deadline.")
		}
		if known(l.Until) {
			if _, err := time.Parse(dateLayout, l.Until.ValueString()); err != nil {
				d.AddAttributeError(p.AtName("until"), "Invalid date", "until must be a real calendar date written YYYY-MM-DD.")
			}
		}
	}
	var start, end time.Time
	var okStart, okEnd bool
	if known(m.StartDate) {
		t, err := time.Parse(dateLayout, m.StartDate.ValueString())
		switch {
		case err != nil:
			d.AddAttributeError(path.Root("start_date"), "Invalid date", "start_date must be a real calendar date written YYYY-MM-DD.")
		case t.Before(mustDate(minStartDate)):
			d.AddAttributeError(path.Root("start_date"), "Date too early", fmt.Sprintf("start_date must not be before %s.", minStartDate))
		default:
			start, okStart = t, true
		}
	}
	if known(m.EndDate) {
		t, err := time.Parse(dateLayout, m.EndDate.ValueString())
		if err != nil {
			d.AddAttributeError(path.Root("end_date"), "Invalid date", "end_date must be a real calendar date written YYYY-MM-DD.")
		} else {
			end, okEnd = t, true
		}
	}
	if okStart && okEnd && end.Before(start) {
		d.AddAttributeError(path.Root("end_date"), "end_date is before start_date", "end_date is the last day the commitment applies and must not be before start_date.")
	}
	return d
}

func mustDate(s string) time.Time {
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		panic(err)
	}
	return t
}
