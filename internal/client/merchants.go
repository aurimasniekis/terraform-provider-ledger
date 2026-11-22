package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// Error codes of the merchant record routes (ADR-0058 of go-ledger).
const (
	CodeMerchantAliasTaken = "merchant_alias_taken"
	CodeMerchantArchived   = "merchant_archived"
	CodeMerchantInUse      = "merchant_in_use"
)

// MerchantAlias is a name transactions are matched to a merchant by. Key is the name as matched (NFC, white space
// collapsed, lower case).
type MerchantAlias struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Key       string `json:"key"`
	Origin    string `json:"origin"`
	CreatedAt string `json:"created_at"`
}

// Merchant is a merchant record as the Management API returns it. The routes need the administrator-only
// merchants:read / merchants:write grants.
type Merchant struct {
	ID          string          `json:"id"`
	DisplayName string          `json:"display_name"`
	Aliases     []MerchantAlias `json:"aliases"`
	Archived    bool            `json:"archived"`
	ArchivedAt  *string         `json:"archived_at"`
	MergedInto  *string         `json:"merged_into"`
	Version     int64           `json:"version"`
	CreatedAt   string          `json:"created_at"`
	UpdatedAt   string          `json:"updated_at"`
}

// CreateMerchant creates a merchant; its display name becomes its first alias.
func (c *Client) CreateMerchant(ctx context.Context, displayName string) (Merchant, error) {
	var m Merchant
	err := c.do(ctx, request{Method: http.MethodPost, Path: "/merchants", Body: map[string]any{"display_name": displayName}, Out: &m})
	return m, err
}

// GetMerchant reads one merchant.
func (c *Client) GetMerchant(ctx context.Context, id string) (Merchant, error) {
	if err := checkID("id", id); err != nil {
		return Merchant{}, err
	}
	var m Merchant
	err := c.do(ctx, request{Method: http.MethodGet, Path: "/merchants/" + id, Out: &m})
	return m, err
}

// ListMerchants reads every page of the merchant list. The API lists active merchants only, unless includeArchived is
// set: then archived and merged ones are listed too (its archived=true filter), in identifier order.
func (c *Client) ListMerchants(ctx context.Context, includeArchived bool) ([]Merchant, error) {
	q := url.Values{"limit": {"200"}}
	if includeArchived {
		q.Set("archived", strconv.FormatBool(true))
	}
	var all []Merchant
	for {
		var page struct {
			Items      []Merchant `json:"items"`
			NextCursor *string    `json:"next_cursor"`
		}
		if err := c.do(ctx, request{Method: http.MethodGet, Path: "/merchants", Query: q, Out: &page}); err != nil {
			return nil, err
		}
		all = append(all, page.Items...)
		if page.NextCursor == nil || *page.NextCursor == "" || len(page.Items) == 0 {
			return all, nil
		}
		q.Set("cursor", *page.NextCursor)
	}
}

// RenameMerchant changes the display name, based on version. The new name also becomes an alias.
func (c *Client) RenameMerchant(ctx context.Context, id string, version int64, displayName string) (Merchant, error) {
	if err := checkID("id", id); err != nil {
		return Merchant{}, err
	}
	var m Merchant
	err := c.do(ctx, request{Method: http.MethodPatch, Path: "/merchants/" + id, Version: version, Body: map[string]any{"display_name": displayName}, Out: &m})
	return m, err
}

// ArchiveMerchant archives a merchant; it keeps every assignment and alias.
func (c *Client) ArchiveMerchant(ctx context.Context, id string, version int64) (Merchant, error) {
	return c.merchantTransition(ctx, id, version, "archive")
}

// UnarchiveMerchant makes an archived merchant usable again (a merged one stays merged).
func (c *Client) UnarchiveMerchant(ctx context.Context, id string, version int64) (Merchant, error) {
	return c.merchantTransition(ctx, id, version, "unarchive")
}

func (c *Client) merchantTransition(ctx context.Context, id string, version int64, verb string) (Merchant, error) {
	if err := checkID("id", id); err != nil {
		return Merchant{}, err
	}
	var m Merchant
	err := c.do(ctx, request{Method: http.MethodPost, Path: "/merchants/" + id + "/" + verb, Version: version, Out: &m})
	return m, err
}

// DeleteMerchant deletes a merchant based on version. The API refuses with merchant_in_use when decisions, assignments
// or merged merchants still refer to it.
func (c *Client) DeleteMerchant(ctx context.Context, id string, version int64) error {
	if err := checkID("id", id); err != nil {
		return err
	}
	return c.do(ctx, request{Method: http.MethodDelete, Path: "/merchants/" + id, Version: version})
}

// AddMerchantAlias adds an alias name to a merchant and returns the merchant. Adding a name the merchant already has
// is not an error; a name another merchant has is merchant_alias_taken.
func (c *Client) AddMerchantAlias(ctx context.Context, id, name string) (Merchant, error) {
	if err := checkID("id", id); err != nil {
		return Merchant{}, err
	}
	var m Merchant
	err := c.do(ctx, request{Method: http.MethodPost, Path: "/merchants/" + id + "/aliases", Body: map[string]any{"name": name}, Out: &m})
	return m, err
}

// RemoveMerchantAlias removes one alias and returns the merchant. It is a 404 when the alias is not there.
func (c *Client) RemoveMerchantAlias(ctx context.Context, id, aliasID string) (Merchant, error) {
	if err := checkID("id", id); err != nil {
		return Merchant{}, err
	}
	if err := checkID("alias_id", aliasID); err != nil {
		return Merchant{}, err
	}
	var m Merchant
	err := c.do(ctx, request{Method: http.MethodDelete, Path: "/merchants/" + id + "/aliases/" + aliasID, Out: &m})
	return m, err
}
