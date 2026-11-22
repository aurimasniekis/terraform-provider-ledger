package provider

import (
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aurimasniekis/terraform-provider-ledger/internal/client"
)

func monthlyModel() commitmentModel {
	return commitmentModel{
		Name: types.StringValue("Rent"), Kind: types.StringValue("service"),
		Amount: types.StringValue("850.00"), Currency: types.StringValue("EUR"),
		Schedule:  &scheduleModel{Frequency: types.StringValue("monthly"), Interval: types.Int64Value(1), Month: types.Int64Null(), Day: types.Int64Value(1)},
		StartDate: types.StringValue("2026-01-01"), EndDate: types.StringNull(), Priority: types.StringNull(),
		Notes: types.StringNull(), MatchTolerance: types.StringNull(),
	}
}

func errorPaths(m commitmentModel) string {
	var s []string
	for _, d := range validateCommitment(m) {
		s = append(s, d.Summary())
	}
	return strings.Join(s, "|")
}

func TestValidateCommitment(t *testing.T) {
	if got := errorPaths(monthlyModel()); got != "" {
		t.Fatalf("a valid monthly commitment was rejected: %s", got)
	}
	cases := map[string]func(*commitmentModel){
		"Missing schedule interval":                  func(m *commitmentModel) { m.Schedule.Interval = types.Int64Null() },
		"Month does not apply to a monthly schedule": func(m *commitmentModel) { m.Schedule.Month = types.Int64Value(2) },
		"Missing schedule month": func(m *commitmentModel) {
			m.Schedule.Frequency = types.StringValue("annual")
			m.Schedule.Interval = types.Int64Null()
		},
		"Interval does not apply to an annual schedule": func(m *commitmentModel) {
			m.Schedule.Frequency = types.StringValue("annual")
			m.Schedule.Month = types.Int64Value(2)
		},
		"Invalid date":                  func(m *commitmentModel) { m.StartDate = types.StringValue("2026-02-30") },
		"Date too early":                func(m *commitmentModel) { m.StartDate = types.StringValue("1899-12-31") },
		"end_date is before start_date": func(m *commitmentModel) { m.EndDate = types.StringValue("2025-12-31") },
		"Missing leeway days": func(m *commitmentModel) {
			m.Leeway = &leewayModel{Kind: types.StringValue("period"), Days: types.Int64Null(), Until: types.StringNull()}
		},
		"Days do not apply": func(m *commitmentModel) {
			m.Leeway = &leewayModel{Kind: types.StringValue("none"), Days: types.Int64Value(3), Until: types.StringNull()}
		},
		"Missing leeway deadline": func(m *commitmentModel) {
			m.Leeway = &leewayModel{Kind: types.StringValue("deadline"), Days: types.Int64Null(), Until: types.StringNull()}
		},
		"Deadline does not apply": func(m *commitmentModel) {
			m.Leeway = &leewayModel{Kind: types.StringValue("unknown"), Days: types.Int64Null(), Until: types.StringValue("2026-05-01")}
		},
	}
	for want, mutate := range cases {
		m := monthlyModel()
		mutate(&m)
		if got := errorPaths(m); !strings.Contains(got, want) {
			t.Errorf("want %q, got %q", want, got)
		}
	}
}

func TestValidateCommitmentSkipsUnknownValues(t *testing.T) {
	m := monthlyModel()
	m.Schedule.Frequency = types.StringUnknown()
	m.Schedule.Interval = types.Int64Unknown()
	m.StartDate = types.StringUnknown()
	m.EndDate = types.StringUnknown()
	m.Leeway = &leewayModel{Kind: types.StringUnknown(), Days: types.Int64Unknown()}
	if got := errorPaths(m); got != "" {
		t.Fatalf("unknown values must not be judged: %s", got)
	}
}

func TestCommitmentMappingRoundTripKeepsExactStrings(t *testing.T) {
	one, d, days := int64(1), int64(29), int64(5)
	two := int64(2)
	prio, note, cond := "essential", "n", "after notice"
	in := client.Commitment{
		ID: "01JD3Y4C8V9M2Q7W5R6T0XZBNA", Name: "Domain", Kind: "domain",
		Amount:    client.Money{Amount: "12.50", Currency: "USD"},
		Schedule:  client.Schedule{Version: 1, Frequency: "annual", Month: &two, Day: d},
		StartDate: "2026-02-28", Priority: &prio, Notes: &note,
		MatchTolerance: &client.Money{Amount: "0.00", Currency: "USD"},
		Leeway:         &client.Leeway{Kind: "period", Days: &days, Conditions: &cond},
		Version:        3, CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-02T00:00:00Z",
	}
	_ = one
	m := commitmentFromAPI(in)
	if m.Amount.ValueString() != "12.50" || m.MatchTolerance.ValueString() != "0.00" || !m.Schedule.Interval.IsNull() || m.Schedule.Month.ValueInt64() != 2 {
		t.Fatalf("mapping lost exactness or form: %+v", m)
	}
	out := m.input()
	if out.Amount != in.Amount || out.MatchTolerance == nil || *out.MatchTolerance != *in.MatchTolerance {
		t.Fatalf("amounts changed on the way back: %+v", out)
	}
	if out.Schedule.Interval != nil || out.Schedule.Month == nil || *out.Schedule.Month != 2 || out.Schedule.Version != 1 {
		t.Fatalf("schedule = %+v", out.Schedule)
	}
	if out.Leeway == nil || out.Leeway.Kind != "period" || *out.Leeway.Days != 5 || out.Leeway.Until != nil {
		t.Fatalf("leeway = %+v", out.Leeway)
	}
	if out.EndDate != nil {
		t.Fatalf("end_date = %v, want nil", out.EndDate)
	}
}

func TestInputDropsMembersOfTheOtherScheduleForm(t *testing.T) {
	m := monthlyModel()
	m.Schedule.Frequency = types.StringValue("annual")
	m.Schedule.Month = types.Int64Value(11)
	// A stale interval left in a plan must not travel with an annual schedule.
	got := m.input().Schedule
	if got.Interval != nil || got.Month == nil || *got.Month != 11 {
		t.Fatalf("schedule = %+v", got)
	}
}

func TestAmountPatterns(t *testing.T) {
	ok := []string{"1", "850.00", "0.01", "12.5000"}
	bad := []string{"0", "0.00", "-1", "1e3", "1,5", ".5", "01", "", " 1"}
	for _, s := range ok {
		if !positiveDecimalRegexp.MatchString(s) {
			t.Errorf("%q should be a positive decimal", s)
		}
	}
	for _, s := range bad {
		if positiveDecimalRegexp.MatchString(s) {
			t.Errorf("%q should not be a positive decimal", s)
		}
	}
	for s, want := range map[string]bool{"0": true, "0.00": true, "0.5": true, "-0.5": false, "00": false, "1e2": false} {
		if nonNegDecimalRegexp.MatchString(s) != want {
			t.Errorf("non-negative %q: want %v", s, want)
		}
	}
}
