package acceptance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/aurimasniekis/terraform-provider-ledger/internal/client"
	"github.com/aurimasniekis/terraform-provider-ledger/internal/ledgertest"
)

// The passwords below are synthetic test values for a throwaway local Ledger.
const (
	accPassword1 = "acceptance-password-one"
	accPassword2 = "acceptance-password-two"
)

// userFixture starts a local Ledger and points the provider at it with the bootstrap root token.
func userFixture(t *testing.T) (*ledgertest.Server, *client.Client) {
	t.Helper()
	s := ledgertest.Start(t)
	t.Setenv("LEDGER_ENDPOINT", s.URL)
	t.Setenv("LEDGER_TOKEN", s.Token.Reveal())
	c, err := client.New(client.Config{Endpoint: s.URL, Token: s.Token})
	if err != nil {
		t.Fatal(err)
	}
	return s, c
}

// postJSON sends a JSON body with a bearer token (or none) and returns the status and the answer.
func postJSON(t *testing.T, s *ledgertest.Server, path, bearer string, body any) (int, []byte) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, s.URL+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, out
}

// signInStatus tries a password sign-in, which is how the test proves what password Ledger holds.
func signInStatus(t *testing.T, s *ledgertest.Server, username, password string) int {
	t.Helper()
	code, _ := postJSON(t, s, "/api/v1/auth/sign-in", "", map[string]string{"username": username, "password": password})
	return code
}

func userConfig(username, role, display string, disabled bool, password string, version int) string {
	d := ""
	if display != "" {
		d = fmt.Sprintf("  display_name = %q\n", display)
	}
	cfg := fmt.Sprintf(`
resource "ledger_user" "member" {
  username = %q
  role     = %q
  disabled = %t
%s}

data "ledger_user" "by_name" {
  username   = ledger_user.member.username
  depends_on = [ledger_user.member]
}

data "ledger_users" "everyone" {
  depends_on = [ledger_user.member]
}
`, username, role, disabled, d)
	if password != "" {
		cfg += fmt.Sprintf(`
resource "ledger_user_password" "member" {
  user_id             = ledger_user.member.id
  password_wo         = %q
  password_wo_version = %d
}
`, password, version)
	}
	return cfg
}

func TestUserResources(t *testing.T) {
	s, c := userFixture(t)
	ctx := context.Background()
	userByName := func(name string) (client.User, error) {
		all, err := c.ListUsers(ctx, client.UserFilter{UsernamePrefix: name})
		if err != nil {
			return client.User{}, err
		}
		for _, u := range all {
			if u.Username == name {
				return u, nil
			}
		}
		return client.User{}, fmt.Errorf("no user %q", name)
	}
	expectSignIn := func(username, password string, want int) resource.TestCheckFunc {
		return func(*terraform.State) error {
			if got := signInStatus(t, s, username, password); got != want {
				return fmt.Errorf("sign-in as %s: HTTP %d, want %d", username, got, want)
			}
			return nil
		}
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: providerFactories,
		CheckDestroy: func(*terraform.State) error {
			u, err := userByName("dana2")
			if err != nil {
				return err
			}
			if !u.Disabled || u.DisabledAt == nil {
				return fmt.Errorf("destroy must disable the user, not leave it enabled: %+v", u)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: userConfig("dana", "read-only", "Dana D", false, accPassword1, 1), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("ledger_user.member", "role", "read-only"),
				resource.TestCheckResourceAttr("ledger_user.member", "display_name", "Dana D"),
				resource.TestCheckResourceAttr("ledger_user.member", "disabled", "false"),
				resource.TestCheckResourceAttrPair("data.ledger_user.by_name", "id", "ledger_user.member", "id"),
				resource.TestCheckResourceAttr("data.ledger_user.by_name", "role", "read-only"),
				resource.TestCheckResourceAttr("data.ledger_users.everyone", "items.#", "2"),
				resource.TestCheckNoResourceAttr("ledger_user_password.member", "password_wo"),
				expectSignIn("dana", accPassword1, http.StatusOK),
				expectSignIn("dana", "not-the-password-at-all", http.StatusUnauthorized),
			)},
			// Rename, promote (the root token may), clear the display name; the password stays.
			{Config: userConfig("dana2", "administrator", "", false, accPassword1, 1), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("ledger_user.member", "username", "dana2"),
				resource.TestCheckResourceAttr("ledger_user.member", "role", "administrator"),
				resource.TestCheckNoResourceAttr("ledger_user.member", "display_name"),
				expectSignIn("dana2", accPassword1, http.StatusOK),
			)},
			// A new password value alone changes nothing at Ledger; a raised version applies it.
			{Config: userConfig("dana2", "administrator", "", false, accPassword2, 1), PlanOnly: true},
			{Config: userConfig("dana2", "administrator", "", false, accPassword2, 2), Check: resource.ComposeAggregateTestCheckFunc(
				expectSignIn("dana2", accPassword2, http.StatusOK),
				expectSignIn("dana2", accPassword1, http.StatusUnauthorized),
			)},
			{ResourceName: "ledger_user.member", ImportState: true, ImportStateVerify: true},
			// Disabling: the account can no longer sign in.
			{Config: userConfig("dana2", "administrator", "", true, accPassword2, 2), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("ledger_user.member", "disabled", "true"),
				resource.TestCheckResourceAttrSet("ledger_user.member", "disabled_at"),
				expectSignIn("dana2", accPassword2, http.StatusUnauthorized),
			)},
		},
	})
}

// A token that is not the root token may manage read-only users but not administrators or passwords (decision D32).
func TestUserRootTokenBoundary(t *testing.T) {
	s, _ := userFixture(t)
	code, body := postJSON(t, s, "/api/v1/auth/tokens", s.Token.Reveal(), map[string]any{"name": "users-only", "grants": []string{"users:read", "users:write"}})
	if code != http.StatusCreated {
		t.Fatalf("creating a limited token: HTTP %d", code)
	}
	var created struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(body, &created); err != nil || created.Secret == "" {
		t.Fatal("the limited token answer carries no secret")
	}
	t.Setenv("LEDGER_TOKEN", created.Secret)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: providerFactories,
		Steps: []resource.TestStep{
			// Creating a read-only user is allowed for the limited token.
			{Config: `
resource "ledger_user" "reader" {
  username = "erin"
  role     = "read-only"
}
`, Check: resource.TestCheckResourceAttr("ledger_user.reader", "role", "read-only")},
			// Promoting needs the root token; the message says so and nothing changes.
			{Config: `
resource "ledger_user" "reader" {
  username = "erin"
  role     = "administrator"
}
`, ExpectError: regexp.MustCompile(`(?s)root_token_required.*root\s+token`)},
			// So does setting a password, even for a read-only user.
			{Config: `
resource "ledger_user" "reader" {
  username = "erin"
  role     = "read-only"
}

resource "ledger_user_password" "reader" {
  user_id             = ledger_user.reader.id
  password_wo         = "acceptance-password-three"
  password_wo_version = 1
}
`, ExpectError: regexp.MustCompile(`(?s)Setting the password failed.*root_token_required`)},
		},
	})
}
