package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/aurimasniekis/terraform-provider-ledger/internal/client"
)

const unitPassword = "unit-test-password-value-1"

// fakeUsers stands in for the user endpoints. It enforces If-Match, refuses to disable or demote the last enabled
// administrator and, when rootOnly is set, answers 403 root_token_required for the operations that need the root token.
type fakeUsers struct {
	mu        sync.Mutex
	u         map[string]*client.User
	order     []string
	next      int
	rootOnly  bool
	passwords map[string]string // last password set per user, for the test's own assertions
	pwCalls   int
	patches   []string
}

func (f *fakeUsers) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	send := func(code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	fail := func(status int, code string) {
		send(status, map[string]any{"code": code, "message": code + " by the fake", "request_id": "R"})
	}
	if r.Header.Get("Authorization") != "Bearer unit-token" {
		http.Error(w, "no", http.StatusUnauthorized)
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	p := strings.TrimPrefix(r.URL.Path, "/api/v1")
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if parts[0] != "users" {
		fail(404, "not_found")
		return
	}
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodPost:
			var b struct {
				Username    string  `json:"username"`
				DisplayName *string `json:"display_name"`
				Role        string  `json:"role"`
			}
			_ = json.NewDecoder(r.Body).Decode(&b)
			if f.rootOnly && b.Role == client.RoleAdministrator {
				fail(403, client.CodeRootTokenRequired)
				return
			}
			for _, o := range f.u {
				if o.Username == b.Username {
					fail(409, client.CodeConflict)
					return
				}
			}
			f.next++
			u := &client.User{ID: fmt.Sprintf("01JD3Y4C8V9M2Q7W5R6T0XZ%03d", f.next), Username: b.Username, DisplayName: b.DisplayName, Role: b.Role, Version: 1, CreatedAt: now, UpdatedAt: now}
			f.u[u.ID] = u
			f.order = append(f.order, u.ID)
			send(201, u)
		case http.MethodGet:
			items := []*client.User{}
			for _, id := range f.order {
				u := f.u[id]
				if d := r.URL.Query().Get("disabled"); d != "" && d != fmt.Sprint(u.Disabled) {
					continue
				}
				if pre := r.URL.Query().Get("username[prefix]"); pre != "" && !strings.HasPrefix(u.Username, pre) {
					continue
				}
				if roles := r.URL.Query()["role"]; len(roles) > 0 {
					ok := false
					for _, ro := range roles {
						ok = ok || ro == u.Role
					}
					if !ok {
						continue
					}
				}
				items = append(items, u)
			}
			send(200, map[string]any{"items": items, "next_cursor": nil})
		}
		return
	}
	u := f.u[parts[1]]
	if u == nil {
		fail(404, "not_found")
		return
	}
	if len(parts) == 3 && parts[2] == "password" && r.Method == http.MethodPut {
		if f.rootOnly {
			fail(403, client.CodeRootTokenRequired)
			return
		}
		var b struct {
			Password string `json:"password"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		f.passwords[u.ID] = b.Password
		f.pwCalls++
		send(200, map[string]any{"sessions_ended": 1})
		return
	}
	if r.Method == http.MethodGet {
		send(200, u)
		return
	}
	if r.Method != http.MethodPatch {
		fail(405, "method_not_allowed")
		return
	}
	if r.Header.Get("If-Match") != fmt.Sprintf("\"%d\"", u.Version) {
		fail(409, client.CodeVersionConflict)
		return
	}
	var raw map[string]json.RawMessage
	_ = json.NewDecoder(r.Body).Decode(&raw)
	keys := []string{}
	for k := range raw {
		keys = append(keys, k)
	}
	f.patches = append(f.patches, strings.Join(sorted(keys), ","))
	if v, ok := raw["username"]; ok {
		_ = json.Unmarshal(v, &u.Username)
	}
	if v, ok := raw["display_name"]; ok {
		var s *string
		_ = json.Unmarshal(v, &s)
		u.DisplayName = s
	}
	if v, ok := raw["role"]; ok {
		var role string
		_ = json.Unmarshal(v, &role)
		if f.rootOnly && role == client.RoleAdministrator {
			fail(403, client.CodeRootTokenRequired)
			return
		}
		u.Role = role
	}
	if v, ok := raw["disabled"]; ok {
		var d bool
		_ = json.Unmarshal(v, &d)
		if d && !u.Disabled && u.Role == client.RoleAdministrator && f.enabledAdmins() == 1 {
			fail(409, client.CodeConflict)
			return
		}
		u.Disabled = d
		if d {
			u.DisabledAt = &now
		} else {
			u.DisabledAt = nil
		}
	}
	u.Version++
	u.UpdatedAt = now
	send(200, u)
}

func (f *fakeUsers) enabledAdmins() int {
	n := 0
	for _, u := range f.u {
		if u.Role == client.RoleAdministrator && !u.Disabled {
			n++
		}
	}
	return n
}

func sorted(s []string) []string {
	for i := range s {
		for j := i + 1; j < len(s); j++ {
			if s[j] < s[i] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
	return s
}

func newFakeUsers(t *testing.T) (*fakeUsers, map[string]func() (tfprotov6.ProviderServer, error)) {
	t.Helper()
	requireTerraform(t)
	f := &fakeUsers{u: map[string]*client.User{}, passwords: map[string]string{}}
	srv := httptest.NewServer(withAPIDocument(f))
	t.Cleanup(srv.Close)
	t.Setenv("LEDGER_ENDPOINT", srv.URL)
	t.Setenv("LEDGER_TOKEN", "unit-token")
	return f, map[string]func() (tfprotov6.ProviderServer, error){"ledger": providerserver.NewProtocol6WithError(New("test")())}
}

func userCfg(username, role, display string, disabled bool) string {
	d := ""
	if display != "" {
		d = fmt.Sprintf("  display_name = %q\n", display)
	}
	return fmt.Sprintf("resource \"ledger_user\" \"u\" {\n  username = %q\n  role     = %q\n  disabled = %t\n%s}\n", username, role, disabled, d)
}

func passwordCfg(pw string, version int) string {
	return fmt.Sprintf("resource \"ledger_user_password\" \"p\" {\n  user_id             = ledger_user.u.id\n  password_wo         = %q\n  password_wo_version = %d\n}\n", pw, version)
}

// noSecretInState fails when the password appears anywhere in the Terraform state.
func noSecretInState(secret string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		for name, rs := range s.RootModule().Resources {
			for k, v := range rs.Primary.Attributes {
				if strings.Contains(v, secret) {
					return fmt.Errorf("%s.%s holds the password", name, k)
				}
			}
		}
		return nil
	}
}

func TestUserLifecycleAgainstFakeLedger(t *testing.T) {
	f, factories := newFakeUsers(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		CheckDestroy: func(*terraform.State) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.u) != 1 {
				return fmt.Errorf("a destroyed user must stay in Ledger, found %d", len(f.u))
			}
			for _, u := range f.u {
				if !u.Disabled {
					return fmt.Errorf("destroy must disable the user: %+v", u)
				}
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: userCfg("alice", "read-only", "Alice A", false) + passwordCfg(unitPassword, 1), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("ledger_user.u", "username", "alice"),
				resource.TestCheckResourceAttr("ledger_user.u", "role", "read-only"),
				resource.TestCheckResourceAttr("ledger_user.u", "display_name", "Alice A"),
				resource.TestCheckResourceAttr("ledger_user.u", "disabled", "false"),
				resource.TestCheckNoResourceAttr("ledger_user.u", "disabled_at"),
				resource.TestCheckResourceAttrPair("ledger_user_password.p", "user_id", "ledger_user.u", "id"),
				resource.TestCheckResourceAttr("ledger_user_password.p", "password_wo_version", "1"),
				resource.TestCheckResourceAttr("ledger_user_password.p", "sessions_ended", "1"),
				resource.TestCheckNoResourceAttr("ledger_user_password.p", "password_wo"),
				noSecretInState(unitPassword),
				func(*terraform.State) error {
					f.mu.Lock()
					defer f.mu.Unlock()
					if f.pwCalls != 1 {
						return fmt.Errorf("password calls = %d, want 1", f.pwCalls)
					}
					return nil
				},
			)},
			// Rename, new role, clear the display name, all in one PATCH; the password is untouched.
			{Config: userCfg("alice2", "read-only", "", false) + passwordCfg(unitPassword, 1), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("ledger_user.u", "username", "alice2"),
				resource.TestCheckNoResourceAttr("ledger_user.u", "display_name"),
				func(*terraform.State) error {
					f.mu.Lock()
					defer f.mu.Unlock()
					if f.pwCalls != 1 || f.patches[len(f.patches)-1] != "display_name,username" {
						return fmt.Errorf("pwCalls=%d patches=%v", f.pwCalls, f.patches)
					}
					return nil
				},
			)},
			// A different password value with the same version does nothing; no plan difference.
			{Config: userCfg("alice2", "read-only", "", false) + passwordCfg("another-value-not-applied", 1), PlanOnly: true},
			// A raised version sets it again.
			{Config: userCfg("alice2", "read-only", "", false) + passwordCfg("another-value-applied-2", 2), Check: resource.ComposeAggregateTestCheckFunc(
				noSecretInState("another-value-applied-2"),
				func(*terraform.State) error {
					f.mu.Lock()
					defer f.mu.Unlock()
					for _, pw := range f.passwords {
						if pw != "another-value-applied-2" || f.pwCalls != 2 {
							return fmt.Errorf("password calls = %d", f.pwCalls)
						}
					}
					return nil
				},
			)},
			{ResourceName: "ledger_user.u", ImportState: true, ImportStateVerify: true},
			{Config: userCfg("alice2", "read-only", "", true) + passwordCfg("another-value-applied-2", 2), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("ledger_user.u", "disabled", "true"),
				resource.TestCheckResourceAttrSet("ledger_user.u", "disabled_at"),
			)},
		},
	})
}

func TestUserRootTokenRequiredHint(t *testing.T) {
	f, factories := newFakeUsers(t)
	f.rootOnly = true
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: userCfg("boss", "administrator", "", false), ExpectError: regexp.MustCompile(`(?s)root_token_required.*root\s+token of the root\s+account`)},
			{Config: userCfg("reader", "read-only", "", false) + passwordCfg(unitPassword, 1), ExpectError: regexp.MustCompile(`(?s)Setting the password failed.*root_token_required`)},
		},
	})
}

func TestUserDestroyLastAdministratorIsRefused(t *testing.T) {
	f, factories := newFakeUsers(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: userCfg("root-admin", "administrator", "", false)},
			{Config: userCfg("root-admin", "administrator", "", true), ExpectError: regexp.MustCompile(`(?s)Updating the user failed.*no enabled\s+administrator`)},
			// Let the destroy at the end of the test succeed: a second administrator exists.
			{PreConfig: func() {
				f.mu.Lock()
				f.next++
				id := fmt.Sprintf("01JD3Y4C8V9M2Q7W5R6T0XZ%03d", f.next)
				f.u[id] = &client.User{ID: id, Username: "second", Role: client.RoleAdministrator, Version: 1}
				f.order = append(f.order, id)
				f.mu.Unlock()
			}, Config: userCfg("root-admin", "administrator", "", false)},
		},
	})
}

func TestUserCreateDisableFailureDiagnosticHasRecovery(t *testing.T) {
	f, factories := newFakeUsers(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			// The only administrator cannot be disabled, so the disable step after the create fails.
			{Config: userCfg("solo", "administrator", "", true), ExpectError: regexp.MustCompile(
				`(?s)Disabling the new user failed.*not\s+disabled.*Do\s+not\s+apply\s+again.*terraform\s+untaint\s+ledger_user\.<name>.*terraform\s+state\s+rm\s+ledger_user\.<name>.*terraform\s+import\s+ledger_user\.<name>\s+01JD`)},
			// A second administrator lets the cleanup destroy of the tainted user succeed.
			{PreConfig: func() {
				f.mu.Lock()
				f.next++
				id := fmt.Sprintf("01JD3Y4C8V9M2Q7W5R6T0XZ%03d", f.next)
				f.u[id] = &client.User{ID: id, Username: "second", Role: client.RoleAdministrator, Version: 1}
				f.order = append(f.order, id)
				f.mu.Unlock()
			}, Config: userCfg("solo", "administrator", "", true), ExpectError: regexp.MustCompile(`(?s)Creating the user failed|taken|already`)},
		},
	})
}

func TestUserDataSources(t *testing.T) {
	f, factories := newFakeUsers(t)
	f.mu.Lock()
	for i, spec := range [][3]string{{"ann", "administrator", "false"}, {"andy", "read-only", "false"}, {"bob", "read-only", "true"}} {
		f.next++
		id := fmt.Sprintf("01JD3Y4C8V9M2Q7W5R6T0XZ%03d", f.next)
		f.u[id] = &client.User{ID: id, Username: spec[0], Role: spec[1], Disabled: spec[2] == "true", Version: int64(i + 1)}
		f.order = append(f.order, id)
	}
	f.mu.Unlock()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{{
			Config: `
data "ledger_user" "by_name" { username = "bob" }
data "ledger_user" "by_id" { id = data.ledger_user.by_name.id }
data "ledger_users" "all" {}
data "ledger_users" "readers" {
  roles           = ["read-only"]
  disabled        = false
  username_prefix = "an"
}
`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.ledger_user.by_name", "role", "read-only"),
				resource.TestCheckResourceAttr("data.ledger_user.by_name", "disabled", "true"),
				resource.TestCheckResourceAttr("data.ledger_user.by_id", "username", "bob"),
				resource.TestCheckResourceAttr("data.ledger_users.all", "items.#", "3"),
				resource.TestCheckResourceAttr("data.ledger_users.readers", "items.#", "1"),
				resource.TestCheckResourceAttr("data.ledger_users.readers", "items.0.username", "andy"),
			),
		}, {
			Config:      `data "ledger_user" "missing" { username = "nobody" }`,
			ExpectError: regexp.MustCompile(`No such user`),
		}},
	})
}

func TestUserPasswordRequiresValue(t *testing.T) {
	_, factories := newFakeUsers(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: userCfg("carol", "read-only", "", false) + passwordCfg("", 1), ExpectError: regexp.MustCompile(`A password is required`)},
		},
	})
}

func TestUsernameValidator(t *testing.T) {
	_, factories := newFakeUsers(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: userCfg("Not Valid", "read-only", "", false), ExpectError: regexp.MustCompile(`lower-case`)},
			{Config: userCfg("ok", "owner", "", false), ExpectError: regexp.MustCompile(`role`)},
		},
	})
}
