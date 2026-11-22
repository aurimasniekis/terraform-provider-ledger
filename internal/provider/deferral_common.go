package provider

import (
	"errors"
	"net/http"
	"regexp"
	"sync"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aurimasniekis/terraform-provider-ledger/internal/client"
)

var (
	deferralPlanTypes       = []string{"personal", "creditor_agreed", "documented_grace"}
	deferralPlanStates      = []string{"active", "undone"}
	deferralAdjustmentKinds = []string{"fee", "interest", "other"}
	yearMonthRegexp         = regexp.MustCompile(`^[0-9]{4}-(0[1-9]|1[0-2])$`)
)

func yearMonthValidator() validator.String {
	return stringvalidator.RegexMatches(yearMonthRegexp, "must be a calendar month written YYYY-MM")
}

// planLocks serialises this provider process's writes to one plan. A plan's version moves with every write to it,
// including its adjustments' writes, and Terraform applies sibling resources of one plan in parallel: without the lock
// two of them would read the same version and the second would be refused as stale for no real conflict.
var planLocks sync.Map

func lockPlan(id string) func() {
	m, _ := planLocks.LoadOrStore(id, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// deferralHint adds advice to the refusals of the deferral operations that a person can act on; any other error is
// returned unchanged.
func deferralHint(err error) string {
	var ae *client.APIError
	if !errors.As(err, &ae) || ae.Status != http.StatusConflict {
		return err.Error()
	}
	switch ae.Code {
	case "nothing_outstanding":
		return err.Error() + " Only an occurrence that still owes something can be deferred or have its plan revised."
	case "occurrence_cancelled":
		return err.Error() + " Ledger cancelled this occurrence (for example after a schedule change); look it up again with the ledger_occurrence data source."
	case "deferral_plan_exists":
		return err.Error() + " The occurrence already has an active plan. Import it (terraform import) instead of creating a second one."
	case client.CodeDeferralPlanUndone:
		return err.Error() + " The plan was undone outside Terraform and can no longer change. Run terraform plan to refresh."
	case client.CodeDeferralAdjustmentsUnpaid:
		return err.Error() + " A fee or interest adjustment is still planned: mark it paid (paid = true on its ledger_deferral_plan_adjustment) or remove it first."
	}
	return err.Error()
}

func realDate(v types.String, p path.Path, name string) diag.Diagnostics {
	var d diag.Diagnostics
	if known(v) {
		if _, err := time.Parse(dateLayout, v.ValueString()); err != nil {
			d.AddAttributeError(p, "Invalid date", name+" must be a real calendar date written YYYY-MM-DD.")
		}
	}
	return d
}

func actorVia(a *client.Actor) types.String {
	if a == nil {
		return types.StringNull()
	}
	return strOrNull(a.Via)
}

// deferralAdjustmentModel is one adjustment as the data sources show it. Removed adjustments stay listed with
// removed_at set, because they are history.
type deferralAdjustmentModel struct {
	ID        types.String `tfsdk:"id"`
	Kind      types.String `tfsdk:"kind"`
	Amount    types.String `tfsdk:"amount"`
	Currency  types.String `tfsdk:"currency"`
	Note      types.String `tfsdk:"note"`
	CreatedAt types.String `tfsdk:"created_at"`
	RemovedAt types.String `tfsdk:"removed_at"`
	PaidAt    types.String `tfsdk:"paid_at"`
	PaidVia   types.String `tfsdk:"paid_via"`
}

func deferralAdjustmentFromAPI(a client.DeferralAdjustment) deferralAdjustmentModel {
	return deferralAdjustmentModel{
		ID: types.StringValue(a.ID), Kind: types.StringValue(a.Kind), Amount: types.StringValue(a.Amount.Amount),
		Currency: types.StringValue(a.Amount.Currency), Note: strOrNull(a.Note), CreatedAt: types.StringValue(a.CreatedAt),
		RemovedAt: strOrNull(a.RemovedAt), PaidAt: strOrNull(a.PaidAt), PaidVia: actorVia(a.PaidBy),
	}
}

// deferralPlanModel is the plan as the data sources show it, live derived state included.
type deferralPlanModel struct {
	ID                     types.String              `tfsdk:"id"`
	OccurrenceID           types.String              `tfsdk:"occurrence_id"`
	CommitmentID           types.String              `tfsdk:"commitment_id"`
	PlanType               types.String              `tfsdk:"plan_type"`
	State                  types.String              `tfsdk:"state"`
	PlannedDate            types.String              `tfsdk:"planned_date"`
	PlannedMonth           types.String              `tfsdk:"planned_month"`
	Reason                 types.String              `tfsdk:"reason"`
	Currency               types.String              `tfsdk:"currency"`
	OriginalDueDate        types.String              `tfsdk:"original_due_date"`
	OriginalAmount         types.String              `tfsdk:"original_amount"`
	OutstandingWhenPlanned types.String              `tfsdk:"outstanding_when_planned"`
	OccurrenceStatus       types.String              `tfsdk:"occurrence_status"`
	OutstandingAmount      types.String              `tfsdk:"outstanding_amount"`
	OverdueSince           types.String              `tfsdk:"overdue_since"`
	GraceUntil             types.String              `tfsdk:"grace_until"`
	WithinGrace            types.Bool                `tfsdk:"within_grace"`
	Missed                 types.Bool                `tfsdk:"missed"`
	MissedReason           types.String              `tfsdk:"missed_reason"`
	AdjustmentTotal        types.String              `tfsdk:"adjustment_total"`
	UnpaidAdjustmentTotal  types.String              `tfsdk:"unpaid_adjustment_total"`
	Adjustments            []deferralAdjustmentModel `tfsdk:"adjustments"`
	Version                types.Int64               `tfsdk:"version"`
	CreatedAt              types.String              `tfsdk:"created_at"`
	UpdatedAt              types.String              `tfsdk:"updated_at"`
	UndoneAt               types.String              `tfsdk:"undone_at"`
}

func deferralPlanFromAPI(p client.DeferralPlan) deferralPlanModel {
	m := deferralPlanModel{
		ID: types.StringValue(p.ID), OccurrenceID: types.StringValue(p.OccurrenceID), CommitmentID: types.StringValue(p.CommitmentID),
		PlanType: types.StringValue(p.PlanType), State: types.StringValue(p.State), PlannedDate: types.StringValue(p.PlannedDate),
		PlannedMonth: types.StringValue(p.PlannedMonth), Reason: strOrNull(p.Reason), Currency: types.StringValue(p.OriginalAmount.Currency),
		OriginalDueDate: types.StringValue(p.OriginalDueDate), OriginalAmount: types.StringValue(p.OriginalAmount.Amount),
		OutstandingWhenPlanned: types.StringValue(p.OutstandingWhenPlanned.Amount), OccurrenceStatus: types.StringValue(p.OccurrenceStatus),
		OutstandingAmount: types.StringValue(p.OutstandingAmount.Amount), OverdueSince: strOrNull(p.OverdueSince),
		GraceUntil: strOrNull(p.GraceUntil), WithinGrace: types.BoolValue(p.WithinGrace), Missed: types.BoolValue(p.Missed),
		MissedReason: strOrNull(p.MissedReason), AdjustmentTotal: types.StringValue(p.AdjustmentTotal.Amount),
		UnpaidAdjustmentTotal: types.StringValue(p.UnpaidAdjustmentTotal.Amount), Version: types.Int64Value(p.Version),
		CreatedAt: types.StringValue(p.CreatedAt), UpdatedAt: types.StringValue(p.UpdatedAt), UndoneAt: strOrNull(p.UndoneAt),
		Adjustments: make([]deferralAdjustmentModel, 0, len(p.Adjustments)),
	}
	for _, a := range p.Adjustments {
		m.Adjustments = append(m.Adjustments, deferralAdjustmentFromAPI(a))
	}
	return m
}
