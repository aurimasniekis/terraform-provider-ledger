package client

import (
	"context"
	"net/http"
	"net/url"
)

// OverrideState is the manual status override of an occurrence as its payment state reports it.
type OverrideState struct {
	Status         string  `json:"status"`
	Reason         string  `json:"reason"`
	SetAt          string  `json:"set_at"`
	Applied        bool    `json:"applied"`
	PreviousStatus *string `json:"previous_status"`
}

// ManualPayment is a payment the person recorded by hand: user data, never a bank fact. The amount stays an exact decimal
// string with its currency.
type ManualPayment struct {
	ID                string  `json:"id"`
	OccurrenceID      string  `json:"occurrence_id"`
	Amount            Money   `json:"amount"`
	PaidOn            string  `json:"paid_on"`
	Note              *string `json:"note"`
	Version           int64   `json:"version"`
	CreatedAt         string  `json:"created_at"`
	UpdatedAt         string  `json:"updated_at"`
	ReplacedByMatchID *string `json:"replaced_by_match_id"`
}

// ManualPaymentInput is what a manual payment is recorded with. A nil note is left out.
type ManualPaymentInput struct {
	Amount Money
	PaidOn string
	Note   *string
}

type manualPaymentPage struct {
	Items      []ManualPayment `json:"items"`
	NextCursor *string         `json:"next_cursor"`
}

// RecordManualPayment records a payment by hand against an occurrence. It needs no version.
func (c *Client) RecordManualPayment(ctx context.Context, occurrenceID string, in ManualPaymentInput) (ManualPayment, error) {
	if err := checkID("occurrence id", occurrenceID); err != nil {
		return ManualPayment{}, err
	}
	body := map[string]any{"amount": in.Amount, "paid_on": in.PaidOn}
	if in.Note != nil {
		body["note"] = *in.Note
	}
	var out ManualPayment
	err := c.do(ctx, request{Method: http.MethodPost, Path: "/occurrences/" + occurrenceID + "/manual-payments", Body: body, Out: &out})
	return out, err
}

// GetManualPayment reads one manual payment. A deleted payment is not found.
func (c *Client) GetManualPayment(ctx context.Context, occurrenceID, id string) (ManualPayment, error) {
	if err := checkID("occurrence id", occurrenceID); err != nil {
		return ManualPayment{}, err
	}
	if err := checkID("payment id", id); err != nil {
		return ManualPayment{}, err
	}
	var out ManualPayment
	err := c.do(ctx, request{Method: http.MethodGet, Path: "/occurrences/" + occurrenceID + "/manual-payments/" + id, Out: &out})
	return out, err
}

// ListManualPayments reads every page of an occurrence's manual payments, ordered by paid_on and then identifier.
func (c *Client) ListManualPayments(ctx context.Context, occurrenceID string) ([]ManualPayment, error) {
	if err := checkID("occurrence id", occurrenceID); err != nil {
		return nil, err
	}
	q := url.Values{"limit": {"200"}}
	var all []ManualPayment
	for {
		var page manualPaymentPage
		if err := c.do(ctx, request{Method: http.MethodGet, Path: "/occurrences/" + occurrenceID + "/manual-payments", Query: q, Out: &page}); err != nil {
			return nil, err
		}
		all = append(all, page.Items...)
		if page.NextCursor == nil || *page.NextCursor == "" || len(page.Items) == 0 {
			return all, nil
		}
		q.Set("cursor", *page.NextCursor)
	}
}

// DeleteManualPayment deletes (soft-deletes) a manual payment based on its version. The occurrence's status is derived
// again from the evidence that remains.
func (c *Client) DeleteManualPayment(ctx context.Context, occurrenceID, id string, version int64) error {
	if err := checkID("occurrence id", occurrenceID); err != nil {
		return err
	}
	if err := checkID("payment id", id); err != nil {
		return err
	}
	return c.do(ctx, request{Method: http.MethodDelete, Path: "/occurrences/" + occurrenceID + "/manual-payments/" + id, Version: version})
}

// SetStatusOverride sets, or replaces, the manual status override ("paid" or "unpaid") with its reason, based on the
// occurrence's version. Ledger refuses "paid" without payment evidence (409 payment_evidence_required).
func (c *Client) SetStatusOverride(ctx context.Context, occurrenceID string, occurrenceVersion int64, status, reason string) (Occurrence, error) {
	if err := checkID("occurrence id", occurrenceID); err != nil {
		return Occurrence{}, err
	}
	var out Occurrence
	err := c.do(ctx, request{
		Method: http.MethodPut, Path: "/occurrences/" + occurrenceID + "/status-override", Version: occurrenceVersion,
		Body: map[string]any{"status": status, "reason": reason}, Out: &out,
	})
	return out, err
}

// UndoStatusOverride undoes the last override change based on the occurrence's version: the active override is marked
// undone and the one it replaced, if any, becomes active again. 404 when no override is active.
func (c *Client) UndoStatusOverride(ctx context.Context, occurrenceID string, occurrenceVersion int64) (Occurrence, error) {
	if err := checkID("occurrence id", occurrenceID); err != nil {
		return Occurrence{}, err
	}
	var out Occurrence
	err := c.do(ctx, request{Method: http.MethodDelete, Path: "/occurrences/" + occurrenceID + "/status-override", Version: occurrenceVersion, Out: &out})
	return out, err
}
