package client

import (
	"context"
	"net/http"
	"net/url"
)

// Error codes of the deferral-plan operations (go-ledger ADR-0050).
const (
	CodeDeferralPlanUndone        = "deferral_plan_undone"
	CodeDeferralAdjustmentRemoved = "deferral_adjustment_removed"
	CodeDeferralAdjustmentsUnpaid = "deferral_adjustments_unpaid"
)

// DeferralAdjustment is an explicit fee, interest or other charge recorded on a plan, in the plan's currency. PaidAt is
// the person's own marker, never payment evidence for the occurrence.
type DeferralAdjustment struct {
	ID        string  `json:"id"`
	Kind      string  `json:"kind"`
	Amount    Money   `json:"amount"`
	Note      *string `json:"note"`
	CreatedAt string  `json:"created_at"`
	RemovedAt *string `json:"removed_at"`
	PaidAt    *string `json:"paid_at"`
	PaidBy    *Actor  `json:"paid_by"`
}

// DeferralPlan is the user's plan to pay an outstanding occurrence later. It is never payment: the original_* members
// are history fixed when the plan was made, and OccurrenceStatus, OutstandingAmount, OverdueSince, GraceUntil,
// WithinGrace and Missed are derived on every read.
type DeferralPlan struct {
	ID                     string               `json:"id"`
	OccurrenceID           string               `json:"occurrence_id"`
	CommitmentID           string               `json:"commitment_id"`
	PlanType               string               `json:"plan_type"`
	State                  string               `json:"state"`
	PlannedDate            string               `json:"planned_date"`
	PlannedMonth           string               `json:"planned_month"`
	Reason                 *string              `json:"reason"`
	OriginalDueDate        string               `json:"original_due_date"`
	OriginalAmount         Money                `json:"original_amount"`
	OutstandingWhenPlanned Money                `json:"outstanding_when_planned"`
	OccurrenceStatus       string               `json:"occurrence_status"`
	OutstandingAmount      Money                `json:"outstanding_amount"`
	OverdueSince           *string              `json:"overdue_since"`
	GraceUntil             *string              `json:"grace_until"`
	WithinGrace            bool                 `json:"within_grace"`
	Missed                 bool                 `json:"missed"`
	MissedReason           *string              `json:"missed_reason"`
	AdjustmentTotal        Money                `json:"adjustment_total"`
	UnpaidAdjustmentTotal  Money                `json:"unpaid_adjustment_total"`
	Adjustments            []DeferralAdjustment `json:"adjustments"`
	Version                int64                `json:"version"`
	CreatedAt              string               `json:"created_at"`
	UpdatedAt              string               `json:"updated_at"`
	UndoneAt               *string              `json:"undone_at"`
}

// DeferralPlanEvent is one change of a plan's append-only history, with the plan's type and planned date after it.
type DeferralPlanEvent struct {
	Action       string  `json:"action"`
	PlanVersion  int64   `json:"plan_version"`
	PlanType     string  `json:"plan_type"`
	PlannedDate  string  `json:"planned_date"`
	Reason       *string `json:"reason"`
	AdjustmentID *string `json:"adjustment_id"`
	By           Actor   `json:"by"`
	At           string  `json:"at"`
}

// DeferralPlanInput is the writable part of a plan. A nil Reason is sent as null: not recorded on a create, cleared in
// a revision.
type DeferralPlanInput struct {
	OccurrenceID string
	PlanType     string
	PlannedDate  string
	Reason       *string
}

// DeferralPlanFilter narrows a list. Empty fields are not sent.
type DeferralPlanFilter struct {
	OccurrenceID string
	CommitmentID string
	State        string
	PlannedMonth string
}

// DeferralAdjustmentInput is a fee, interest or other charge to record. The currency must be the plan's: nothing is
// converted.
type DeferralAdjustmentInput struct {
	Kind   string
	Amount Money
	Note   *string
}

type deferralPlanPage struct {
	Items      []DeferralPlan `json:"items"`
	NextCursor *string        `json:"next_cursor"`
}

type deferralEventPage struct {
	Items      []DeferralPlanEvent `json:"items"`
	NextCursor *string             `json:"next_cursor"`
}

// CreateDeferralPlan records a plan to pay an outstanding occurrence later.
func (c *Client) CreateDeferralPlan(ctx context.Context, in DeferralPlanInput) (DeferralPlan, error) {
	if err := checkID("occurrence_id", in.OccurrenceID); err != nil {
		return DeferralPlan{}, err
	}
	var out DeferralPlan
	err := c.do(ctx, request{Method: http.MethodPost, Path: "/deferral-plans", Out: &out, Body: map[string]any{
		"occurrence_id": in.OccurrenceID, "plan_type": in.PlanType, "planned_date": in.PlannedDate, "reason": in.Reason,
	}})
	return out, err
}

// GetDeferralPlan reads one plan with its adjustments.
func (c *Client) GetDeferralPlan(ctx context.Context, id string) (DeferralPlan, error) {
	if err := checkID("id", id); err != nil {
		return DeferralPlan{}, err
	}
	var out DeferralPlan
	err := c.do(ctx, request{Method: http.MethodGet, Path: "/deferral-plans/" + id, Out: &out})
	return out, err
}

// ListDeferralPlans reads every page of a list, in the API's ID order, active and undone plans alike.
func (c *Client) ListDeferralPlans(ctx context.Context, f DeferralPlanFilter) ([]DeferralPlan, error) {
	q := url.Values{"limit": {"200"}}
	for k, v := range map[string]string{"occurrence_id": f.OccurrenceID, "commitment_id": f.CommitmentID, "state": f.State, "planned_month": f.PlannedMonth} {
		if v != "" {
			q.Set(k, v)
		}
	}
	var all []DeferralPlan
	for {
		var page deferralPlanPage
		if err := c.do(ctx, request{Method: http.MethodGet, Path: "/deferral-plans", Query: q, Out: &page}); err != nil {
			return nil, err
		}
		all = append(all, page.Items...)
		if page.NextCursor == nil || *page.NextCursor == "" || len(page.Items) == 0 {
			return all, nil
		}
		q.Set("cursor", *page.NextCursor)
	}
}

// ReviseDeferralPlan sends the whole writable plan (type, date and reason, null clearing the reason) as a merge patch
// based on version. The occurrence cannot change.
func (c *Client) ReviseDeferralPlan(ctx context.Context, id string, version int64, in DeferralPlanInput) (DeferralPlan, error) {
	if err := checkID("id", id); err != nil {
		return DeferralPlan{}, err
	}
	var out DeferralPlan
	err := c.do(ctx, request{Method: http.MethodPatch, Path: "/deferral-plans/" + id, Version: version, Out: &out, Body: map[string]any{
		"plan_type": in.PlanType, "planned_date": in.PlannedDate, "reason": in.Reason,
	}})
	return out, err
}

// UndoDeferralPlan ends an active plan based on version. The plan and its history are kept. The API refuses (409
// deferral_adjustments_unpaid) while a fee is still planned.
func (c *Client) UndoDeferralPlan(ctx context.Context, id string, version int64) (DeferralPlan, error) {
	if err := checkID("id", id); err != nil {
		return DeferralPlan{}, err
	}
	var out DeferralPlan
	err := c.do(ctx, request{Method: http.MethodPost, Path: "/deferral-plans/" + id + "/undo", Version: version, Out: &out})
	return out, err
}

// ListDeferralPlanHistory reads every page of a plan's history, oldest first.
func (c *Client) ListDeferralPlanHistory(ctx context.Context, id string) ([]DeferralPlanEvent, error) {
	if err := checkID("id", id); err != nil {
		return nil, err
	}
	q := url.Values{"limit": {"200"}}
	var all []DeferralPlanEvent
	for {
		var page deferralEventPage
		if err := c.do(ctx, request{Method: http.MethodGet, Path: "/deferral-plans/" + id + "/history", Query: q, Out: &page}); err != nil {
			return nil, err
		}
		all = append(all, page.Items...)
		if page.NextCursor == nil || *page.NextCursor == "" || len(page.Items) == 0 {
			return all, nil
		}
		q.Set("cursor", *page.NextCursor)
	}
}

// AddDeferralAdjustment records a fee, interest or other charge on an active plan based on the plan's version and
// returns the plan with it.
func (c *Client) AddDeferralAdjustment(ctx context.Context, planID string, version int64, in DeferralAdjustmentInput) (DeferralPlan, error) {
	if err := checkID("deferral_plan_id", planID); err != nil {
		return DeferralPlan{}, err
	}
	var out DeferralPlan
	err := c.do(ctx, request{Method: http.MethodPost, Path: "/deferral-plans/" + planID + "/adjustments", Version: version, Out: &out, Body: map[string]any{
		"kind": in.Kind, "amount": in.Amount, "note": in.Note,
	}})
	return out, err
}

// RemoveDeferralAdjustment removes an adjustment of an active plan (it stays as history) and returns the plan.
func (c *Client) RemoveDeferralAdjustment(ctx context.Context, planID, adjustmentID string, version int64) (DeferralPlan, error) {
	return c.adjustmentCall(ctx, http.MethodDelete, planID, adjustmentID, "", version)
}

// MarkDeferralAdjustmentPaid records that the person paid the adjustment. It is the person's own record and not payment
// evidence for the occurrence.
func (c *Client) MarkDeferralAdjustmentPaid(ctx context.Context, planID, adjustmentID string, version int64) (DeferralPlan, error) {
	return c.adjustmentCall(ctx, http.MethodPost, planID, adjustmentID, "/paid", version)
}

// UnmarkDeferralAdjustmentPaid clears the paid marker: the adjustment is planned again.
func (c *Client) UnmarkDeferralAdjustmentPaid(ctx context.Context, planID, adjustmentID string, version int64) (DeferralPlan, error) {
	return c.adjustmentCall(ctx, http.MethodPost, planID, adjustmentID, "/unpaid", version)
}

func (c *Client) adjustmentCall(ctx context.Context, method, planID, adjustmentID, suffix string, version int64) (DeferralPlan, error) {
	if err := checkID("deferral_plan_id", planID); err != nil {
		return DeferralPlan{}, err
	}
	if err := checkID("adjustment id", adjustmentID); err != nil {
		return DeferralPlan{}, err
	}
	var out DeferralPlan
	err := c.do(ctx, request{Method: method, Path: "/deferral-plans/" + planID + "/adjustments/" + adjustmentID + suffix, Version: version, Out: &out})
	return out, err
}

// PaymentState is the part of an occurrence's derived payment state the provider reads. It is derived on every read from
// payment evidence and dates, never stored by Terraform and never converted between currencies.
type PaymentState struct {
	Status                string         `json:"status"`
	DerivedStatus         string         `json:"derived_status"`
	Override              *OverrideState `json:"override"`
	Timing                *string        `json:"timing"`
	PaidAmount            Money          `json:"paid_amount"`
	OutstandingAmount     Money          `json:"outstanding_amount"`
	UnconvertedCurrencies []string       `json:"unconverted_currencies"`
	GraceUntil            *string        `json:"grace_until"`
	WithinGrace           bool           `json:"within_grace"`
}

// Occurrence is one due date of a commitment, derived by Ledger from the definition. It may be rewritten in place by a
// terms change, or cancelled and replaced.
type Occurrence struct {
	ID              string       `json:"id"`
	CommitmentID    string       `json:"commitment_id"`
	Month           string       `json:"month"`
	DueDate         string       `json:"due_date"`
	Amount          Money        `json:"amount"`
	Version         int64        `json:"version"`
	CancelledAt     *string      `json:"cancelled_at"`
	CancelledReason *string      `json:"cancelled_reason"`
	Payment         PaymentState `json:"payment"`
}

type occurrencePage struct {
	Items      []Occurrence `json:"items"`
	NextCursor *string      `json:"next_cursor"`
}

// GetOccurrence reads one occurrence with its payment state today.
func (c *Client) GetOccurrence(ctx context.Context, id string) (Occurrence, error) {
	if err := checkID("id", id); err != nil {
		return Occurrence{}, err
	}
	var out Occurrence
	err := c.do(ctx, request{Method: http.MethodGet, Path: "/occurrences/" + id, Out: &out})
	return out, err
}

// ListOccurrences reads every page of one reporting month's occurrences of a commitment, ordered by due date. Cancelled
// occurrences are included only when includeCancelled is true. Reading a month makes Ledger materialise the
// commitment's occurrences through it.
func (c *Client) ListOccurrences(ctx context.Context, month, commitmentID string, includeCancelled bool) ([]Occurrence, error) {
	if err := checkID("commitment_id", commitmentID); err != nil {
		return nil, err
	}
	q := url.Values{"limit": {"200"}, "month": {month}, "commitment_id": {commitmentID}}
	if includeCancelled {
		q.Set("include_cancelled", "true")
	}
	var all []Occurrence
	for {
		var page occurrencePage
		if err := c.do(ctx, request{Method: http.MethodGet, Path: "/occurrences", Query: q, Out: &page}); err != nil {
			return nil, err
		}
		all = append(all, page.Items...)
		if page.NextCursor == nil || *page.NextCursor == "" || len(page.Items) == 0 {
			return all, nil
		}
		q.Set("cursor", *page.NextCursor)
	}
}
