package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// Error codes of the user routes.
const (
	// CodeRootTokenRequired is the 403 an API token gets for an operation that needs the root token of the root account
	// (creating or promoting an administrator, re-enabling one, setting a password; decisions D32 to D35 of go-ledger).
	CodeRootTokenRequired = "root_token_required"
)

// User roles.
const (
	RoleAdministrator = "administrator"
	RoleReadOnly      = "read-only"
)

// User is a user account as the Management API returns it. It carries no credential.
type User struct {
	ID          string  `json:"id"`
	Username    string  `json:"username"`
	DisplayName *string `json:"display_name"`
	Role        string  `json:"role"`
	Disabled    bool    `json:"disabled"`
	DisabledAt  *string `json:"disabled_at"`
	Version     int64   `json:"version"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
}

// CreateUser creates an enabled user. A nil displayName leaves it out. Creating an administrator with an API token needs
// the root token (403 root_token_required otherwise).
func (c *Client) CreateUser(ctx context.Context, username, role string, displayName *string) (User, error) {
	body := map[string]any{"username": username, "role": role}
	if displayName != nil {
		body["display_name"] = *displayName
	}
	var u User
	err := c.do(ctx, request{Method: http.MethodPost, Path: "/users", Body: body, Out: &u})
	return u, err
}

// GetUser reads one user.
func (c *Client) GetUser(ctx context.Context, id string) (User, error) {
	if err := checkID("id", id); err != nil {
		return User{}, err
	}
	var u User
	err := c.do(ctx, request{Method: http.MethodGet, Path: "/users/" + id, Out: &u})
	return u, err
}

// UserFilter narrows a user list. Zero values do not filter.
type UserFilter struct {
	Roles          []string
	Disabled       *bool
	UsernamePrefix string
}

// ListUsers reads every page of the user list, in identifier order.
func (c *Client) ListUsers(ctx context.Context, f UserFilter) ([]User, error) {
	q := url.Values{"limit": {"200"}}
	for _, r := range f.Roles {
		q.Add("role", r)
	}
	if f.Disabled != nil {
		q.Set("disabled", strconv.FormatBool(*f.Disabled))
	}
	if f.UsernamePrefix != "" {
		q.Set("username[prefix]", f.UsernamePrefix)
	}
	var all []User
	for {
		var page struct {
			Items      []User  `json:"items"`
			NextCursor *string `json:"next_cursor"`
		}
		if err := c.do(ctx, request{Method: http.MethodGet, Path: "/users", Query: q, Out: &page}); err != nil {
			return nil, err
		}
		all = append(all, page.Items...)
		if page.NextCursor == nil || *page.NextCursor == "" || len(page.Items) == 0 {
			return all, nil
		}
		q.Set("cursor", *page.NextCursor)
	}
}

// UserPatch is a JSON Merge Patch of a user: only the members present are sent. DisplayName is nil to leave it alone,
// and points at nil to clear it (sent as null).
type UserPatch struct {
	Username    *string
	DisplayName **string
	Role        *string
	Disabled    *bool
}

func (p UserPatch) body() map[string]any {
	b := map[string]any{}
	if p.Username != nil {
		b["username"] = *p.Username
	}
	if p.DisplayName != nil {
		if *p.DisplayName == nil {
			b["display_name"] = nil
		} else {
			b["display_name"] = **p.DisplayName
		}
	}
	if p.Role != nil {
		b["role"] = *p.Role
	}
	if p.Disabled != nil {
		b["disabled"] = *p.Disabled
	}
	return b
}

// UpdateUser changes a user based on version. Making a user an administrator or re-enabling a disabled administrator
// needs the root token; so does touching the root account. A change that would leave no enabled administrator is a 409.
func (c *Client) UpdateUser(ctx context.Context, id string, version int64, p UserPatch) (User, error) {
	if err := checkID("id", id); err != nil {
		return User{}, err
	}
	var u User
	err := c.do(ctx, request{Method: http.MethodPatch, Path: "/users/" + id, Version: version, Body: p.body(), Out: &u})
	return u, err
}

// SetUserPassword sets a user's password and returns how many sessions the change ended. Setting any password with an API
// token needs the root token. The password is sent once and is never part of an error or a log line.
func (c *Client) SetUserPassword(ctx context.Context, id string, password Secret) (sessionsEnded int64, err error) {
	if err := checkID("id", id); err != nil {
		return 0, err
	}
	var out struct {
		SessionsEnded int64 `json:"sessions_ended"`
	}
	err = c.do(ctx, request{Method: http.MethodPut, Path: "/users/" + id + "/password", Body: map[string]any{"password": password.Reveal()}, Out: &out})
	return out.SessionsEnded, err
}
