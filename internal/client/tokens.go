package client

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// Token statuses.
const (
	TokenActive  = "active"
	TokenExpired = "expired"
	TokenRevoked = "revoked"
)

// APIToken is the metadata of an API token as Ledger returns it. It never carries the token itself: only a hint.
type APIToken struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	OwnerID       string   `json:"owner_id"`
	Root          bool     `json:"root"`
	Grants        []string `json:"grants"`
	Hint          string   `json:"hint"`
	Prefix        string   `json:"prefix"`
	LastChars     string   `json:"last_chars"`
	Status        string   `json:"status"`
	CreatedAt     string   `json:"created_at"`
	ExpiresAt     *string  `json:"expires_at"`
	LastUsedAt    *string  `json:"last_used_at"`
	RevokedAt     *string  `json:"revoked_at"`
	ParentTokenID *string  `json:"parent_token_id"`
}

// CreatedAPIToken is the one answer that carries the token's secret. The secret is a Secret so that no formatting verb
// shows it; callers reveal it only to store it.
type CreatedAPIToken struct {
	Secret  Secret
	Token   APIToken
	Warning *string
}

// NewAPIToken is the request to create a token. Give Grants, or Root for a root token (not both).
type NewAPIToken struct {
	Name      string
	Grants    []string
	Root      bool
	ExpiresAt *time.Time
}

func (n NewAPIToken) body() map[string]any {
	b := map[string]any{"name": n.Name}
	if n.Root {
		b["root"] = true
	} else {
		g := n.Grants
		if g == nil {
			g = []string{}
		}
		b["grants"] = g
	}
	if n.ExpiresAt != nil {
		b["expires_at"] = n.ExpiresAt.UTC().Format(time.RFC3339)
	}
	return b
}

// CreateAPIToken creates a token through POST /auth/tokens, the one token route a bearer token may use, and only the
// root token of the root account (403 forbidden otherwise). The token is created for the root account and its secret is
// returned once.
func (c *Client) CreateAPIToken(ctx context.Context, n NewAPIToken) (CreatedAPIToken, error) {
	var out struct {
		Secret  string   `json:"secret"`
		Token   APIToken `json:"token"`
		Warning *string  `json:"warning"`
	}
	if err := c.do(ctx, request{Method: http.MethodPost, Path: "/auth/tokens", Body: n.body(), Out: &out}); err != nil {
		return CreatedAPIToken{}, err
	}
	return CreatedAPIToken{Secret: Secret(out.Secret), Token: out.Token, Warning: out.Warning}, nil
}

// GetAPIToken reads one token's metadata (needs tokens:read).
func (c *Client) GetAPIToken(ctx context.Context, id string) (APIToken, error) {
	if err := checkID("id", id); err != nil {
		return APIToken{}, err
	}
	var t APIToken
	err := c.do(ctx, request{Method: http.MethodGet, Path: "/tokens/" + id, Out: &t})
	return t, err
}

// APITokenFilter narrows a token list. Zero values do not filter.
type APITokenFilter struct {
	OwnerID string
}

// ListAPITokens reads every page of the token list, in identifier order. Metadata only.
func (c *Client) ListAPITokens(ctx context.Context, f APITokenFilter) ([]APIToken, error) {
	q := url.Values{"limit": {"200"}}
	if f.OwnerID != "" {
		q.Set("owner_id", f.OwnerID)
	}
	var all []APIToken
	for {
		var page struct {
			Items      []APIToken `json:"items"`
			NextCursor *string    `json:"next_cursor"`
		}
		if err := c.do(ctx, request{Method: http.MethodGet, Path: "/tokens", Query: q, Out: &page}); err != nil {
			return nil, err
		}
		all = append(all, page.Items...)
		if page.NextCursor == nil || *page.NextCursor == "" || len(page.Items) == 0 {
			return all, nil
		}
		q.Set("cursor", *page.NextCursor)
	}
}

// RevokeAPIToken revokes a token and, in Ledger, every token created through it (needs tokens:write). Revoking a revoked
// token is a success. Revoking a root token of the root account with an API token needs the root token (403
// root_token_required).
func (c *Client) RevokeAPIToken(ctx context.Context, id string) error {
	if err := checkID("id", id); err != nil {
		return err
	}
	return c.do(ctx, request{Method: http.MethodDelete, Path: "/tokens/" + id})
}
