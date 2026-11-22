package client

import (
	"context"
	"net/http"
	"net/url"
)

// Money is an exact amount with its currency. The amount stays a string, exactly as the API sends and accepts it: it
// keeps its scale ("850.00") and is never converted to a binary floating-point number.
type Money struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

// Schedule is a commitment's versioned schedule spec (go-ledger ADR-0024). Members that do not apply to the frequency
// are null: Interval for annual, Month for monthly.
type Schedule struct {
	Version   int    `json:"version"`
	Frequency string `json:"frequency"`
	Interval  *int64 `json:"interval"`
	Month     *int64 `json:"month"`
	Day       int64  `json:"day"`
}

// Leeway is what the person recorded about the grace a creditor allows after a due date (go-ledger ADR-0052).
type Leeway struct {
	Kind         string  `json:"kind"`
	Days         *int64  `json:"days"`
	Until        *string `json:"until"`
	Conditions   *string `json:"conditions"`
	Consequences *string `json:"consequences"`
	Source       *string `json:"source"`
}

// Commitment is a recurring commitment definition as the Management API returns it. Dated occurrences and payment
// state are not part of it. Dates and timestamps stay strings, exactly as sent.
type Commitment struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Kind           string   `json:"kind"`
	Amount         Money    `json:"amount"`
	Schedule       Schedule `json:"schedule"`
	StartDate      string   `json:"start_date"`
	EndDate        *string  `json:"end_date"`
	Priority       *string  `json:"priority"`
	Notes          *string  `json:"notes"`
	MatchTolerance *Money   `json:"match_tolerance"`
	Leeway         *Leeway  `json:"leeway"`
	Version        int64    `json:"version"`
	CreatedAt      string   `json:"created_at"`
	UpdatedAt      string   `json:"updated_at"`
}

// CommitmentInput is the writable part of a commitment. A nil optional member is sent as JSON null, which on a
// create means "not recorded" and in a merge patch clears the member. Schedule and Leeway are always sent whole, with
// the members of the other schedule form nulled in an update, and the leeway with every member present, so a switch
// between schedule forms or leeway kinds cannot leave a stale member behind (the API merges patches member by member).
type CommitmentInput struct {
	Name           string
	Kind           string
	Amount         Money
	Schedule       Schedule
	StartDate      string
	EndDate        *string
	Priority       *string
	Notes          *string
	MatchTolerance *Money
	Leeway         *Leeway
}

// scheduleBody writes the schedule form. A create refuses a member that does not belong to the form (400), while a merge
// patch needs an explicit null for it to remove what the old form had, so the nulls are sent only when patch is true.
func scheduleBody(s Schedule, patch bool) map[string]any {
	m := map[string]any{"version": s.Version, "frequency": s.Frequency, "day": s.Day}
	switch s.Frequency {
	case "monthly":
		m["interval"] = s.Interval
		if patch {
			m["month"] = nil
		}
	case "annual":
		m["month"] = s.Month
		if patch {
			m["interval"] = nil
		}
	}
	return m
}

func (in CommitmentInput) body(patch bool) map[string]any {
	return map[string]any{
		"name":            in.Name,
		"kind":            in.Kind,
		"amount":          in.Amount,
		"schedule":        scheduleBody(in.Schedule, patch),
		"start_date":      in.StartDate,
		"end_date":        in.EndDate,
		"priority":        in.Priority,
		"notes":           in.Notes,
		"match_tolerance": in.MatchTolerance,
		"leeway":          in.Leeway,
	}
}

// CommitmentFilter narrows a list. Empty fields are not sent.
type CommitmentFilter struct {
	Kinds      []string
	Priorities []string
	Currencies []string
}

type commitmentPage struct {
	Items      []Commitment `json:"items"`
	NextCursor *string      `json:"next_cursor"`
}

// CreateCommitment records a commitment.
func (c *Client) CreateCommitment(ctx context.Context, in CommitmentInput) (Commitment, error) {
	var out Commitment
	err := c.do(ctx, request{Method: http.MethodPost, Path: "/commitments", Body: in.body(false), Out: &out})
	return out, err
}

// GetCommitment reads one commitment.
func (c *Client) GetCommitment(ctx context.Context, id string) (Commitment, error) {
	if err := checkID("id", id); err != nil {
		return Commitment{}, err
	}
	var out Commitment
	err := c.do(ctx, request{Method: http.MethodGet, Path: "/commitments/" + id, Out: &out})
	return out, err
}

// ListCommitments reads every page of a list, in the API's ID order.
func (c *Client) ListCommitments(ctx context.Context, f CommitmentFilter) ([]Commitment, error) {
	q := url.Values{"limit": {"200"}}
	for _, v := range f.Kinds {
		q.Add("kind", v)
	}
	for _, v := range f.Priorities {
		q.Add("priority", v)
	}
	for _, v := range f.Currencies {
		q.Add("currency", v)
	}
	var all []Commitment
	for {
		var page commitmentPage
		if err := c.do(ctx, request{Method: http.MethodGet, Path: "/commitments", Query: q, Out: &page}); err != nil {
			return nil, err
		}
		all = append(all, page.Items...)
		if page.NextCursor == nil || *page.NextCursor == "" || len(page.Items) == 0 {
			return all, nil
		}
		q.Set("cursor", *page.NextCursor)
	}
}

// UpdateCommitment sends the whole writable definition as a merge patch based on version. Changed terms reach the
// unpaid occurrences from today (the API default for effective_from, go-ledger ADR-0027).
func (c *Client) UpdateCommitment(ctx context.Context, id string, version int64, in CommitmentInput) (Commitment, error) {
	if err := checkID("id", id); err != nil {
		return Commitment{}, err
	}
	var out Commitment
	err := c.do(ctx, request{Method: http.MethodPatch, Path: "/commitments/" + id, Version: version, Body: in.body(true), Out: &out})
	return out, err
}

// DeleteCommitment deletes a commitment and its unpaid occurrences based on version. The API refuses (409 conflict)
// while an occurrence has live payment evidence.
func (c *Client) DeleteCommitment(ctx context.Context, id string, version int64) error {
	if err := checkID("id", id); err != nil {
		return err
	}
	return c.do(ctx, request{Method: http.MethodDelete, Path: "/commitments/" + id, Version: version})
}
