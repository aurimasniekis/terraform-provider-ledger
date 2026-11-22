package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// bearerStatus calls an authenticated read route with a token and returns the HTTP status: how a test proves what Ledger
// currently accepts.
func bearerStatus(t *testing.T, base, token string) int {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, base+"/api/v1/me", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode
}

// extractSecret reads the secret from a token-creation answer.
func extractSecret(t *testing.T, body []byte) string {
	t.Helper()
	var created struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(body, &created); err != nil || created.Secret == "" {
		t.Fatal("the token answer carries no secret")
	}
	return created.Secret
}

func tokenAccConfig(name, body string) string {
	return fmt.Sprintf(`
resource "ledger_api_token" "ci" {
  name = %q
%s}

data "ledger_api_token" "ci" {
  id = ledger_api_token.ci.id
}

data "ledger_api_tokens" "active" {
  statuses   = ["active"]
  depends_on = [ledger_api_token.ci]
}
`, name, body)
}

func TestAPITokenResource(t *testing.T) {
	s, _ := userFixture(t)
	var firstSecret, firstID string
	secretWorks := func(addr string, want int) resource.TestCheckFunc {
		return func(st *terraform.State) error {
			rs := st.RootModule().Resources[addr]
			if rs == nil {
				return fmt.Errorf("no %s", addr)
			}
			if got := bearerStatus(t, s.URL, rs.Primary.Attributes["secret"]); got != want {
				return fmt.Errorf("the token answered HTTP %d, want %d", got, want)
			}
			return nil
		}
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: providerFactories,
		CheckDestroy: func(*terraform.State) error {
			if firstSecret == "" {
				return fmt.Errorf("the test never saw a secret")
			}
			if got := bearerStatus(t, s.URL, firstSecret); got != http.StatusUnauthorized {
				return fmt.Errorf("the first token answered HTTP %d after it was replaced, want 401", got)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: tokenAccConfig("ci", "  grants = [\"commitments:read\", \"organisation:read\"]\n"), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("ledger_api_token.ci", "status", "active"),
				resource.TestCheckResourceAttr("ledger_api_token.ci", "root", "false"),
				resource.TestCheckResourceAttr("ledger_api_token.ci", "grants.#", "2"),
				resource.TestCheckResourceAttrSet("ledger_api_token.ci", "secret"),
				resource.TestMatchResourceAttr("ledger_api_token.ci", "secret", regexp.MustCompile(`^ldgr_[0-9A-Za-z]{43}$`)),
				resource.TestCheckResourceAttrSet("ledger_api_token.ci", "hint"),
				resource.TestCheckResourceAttrPair("data.ledger_api_token.ci", "id", "ledger_api_token.ci", "id"),
				resource.TestCheckResourceAttrPair("data.ledger_api_token.ci", "hint", "ledger_api_token.ci", "hint"),
				resource.TestCheckResourceAttrPair("data.ledger_api_token.ci", "owner_id", "ledger_api_token.ci", "owner_id"),
				resource.TestCheckResourceAttr("data.ledger_api_token.ci", "grants.#", "2"),
				resource.TestCheckNoResourceAttr("data.ledger_api_token.ci", "secret"),
				resource.TestCheckResourceAttr("data.ledger_api_tokens.active", "items.#", "2"), // the bootstrap token and this one
				secretWorks("ledger_api_token.ci", http.StatusOK),
				func(st *terraform.State) error {
					rs := st.RootModule().Resources["ledger_api_token.ci"].Primary
					firstSecret, firstID = rs.Attributes["secret"], rs.ID
					return nil
				},
			)},
			{Config: tokenAccConfig("ci", "  grants = [\"commitments:read\", \"organisation:read\"]\n"), PlanOnly: true},
			// A new name replaces the token: Ledger issues another secret and the old one stops working.
			{Config: tokenAccConfig("ci-renamed", "  grants = [\"commitments:read\", \"organisation:read\"]\n"), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("ledger_api_token.ci", "name", "ci-renamed"),
				secretWorks("ledger_api_token.ci", http.StatusOK),
				func(st *terraform.State) error {
					rs := st.RootModule().Resources["ledger_api_token.ci"].Primary
					if rs.ID == firstID || rs.Attributes["secret"] == firstSecret {
						return fmt.Errorf("replacement kept the old identifier or secret")
					}
					if got := bearerStatus(t, s.URL, firstSecret); got != http.StatusUnauthorized {
						return fmt.Errorf("the replaced token answered HTTP %d, want 401", got)
					}
					return nil
				},
			)},
			// A root token with an expiry that Ledger may cap.
			{Config: tokenAccConfig("ci-root", "  root = true\n  expires_at = \"2099-01-01T00:00:00Z\"\n"), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("ledger_api_token.ci", "root", "true"),
				resource.TestCheckNoResourceAttr("ledger_api_token.ci", "grants"),
				resource.TestCheckResourceAttrSet("ledger_api_token.ci", "warning"),
				resource.TestCheckResourceAttr("ledger_api_token.ci", "expires_at", "2099-01-01T00:00:00Z"),
				resource.TestCheckResourceAttrSet("ledger_api_token.ci", "effective_expires_at"),
				secretWorks("ledger_api_token.ci", http.StatusOK),
			)},
			{Config: tokenAccConfig("ci-root", "  root = true\n  expires_at = \"2099-01-01T00:00:00Z\"\n"), PlanOnly: true},
		},
	})
}

// Only the root token of the root account may create tokens (decision D25) or revoke a root token (D34).
func TestAPITokenRootTokenPrerequisite(t *testing.T) {
	s, _ := userFixture(t)
	code, body := postJSON(t, s, "/api/v1/auth/tokens", s.Token.Reveal(), map[string]any{"name": "tokens-admin", "grants": []string{"tokens:read", "tokens:write"}})
	if code != http.StatusCreated {
		t.Fatalf("creating a limited token: HTTP %d", code)
	}
	secret := extractSecret(t, body)
	t.Setenv("LEDGER_TOKEN", secret)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: providerFactories,
		Steps: []resource.TestStep{
			{Config: tokenAccConfig("nope", "  grants = [\"commitments:read\"]\n"),
				ExpectError: regexp.MustCompile(`(?s)Creating the API token failed.*forbidden.*root\s+token of the root\s+account`)},
			// Metadata is readable with tokens:read.
			{Config: `
data "ledger_api_tokens" "all" {}
`, Check: func(st *terraform.State) error {
				n := st.RootModule().Resources["data.ledger_api_tokens.all"].Primary.Attributes["items.#"]
				if n == "" || strings.HasPrefix(n, "0") {
					return fmt.Errorf("items.# = %q", n)
				}
				return nil
			}},
		},
	})
}
