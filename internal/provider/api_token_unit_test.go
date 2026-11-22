package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/aurimasniekis/terraform-provider-ledger/internal/client"
)

// fakeTokens stands in for the token endpoints. Only the bearer "unit-token" is the root token: creating a token with
// any other bearer is 403 forbidden, as at Ledger. Revoking cascades to children and, when revokeRootOnly is set, answers
// 403 root_token_required.
type fakeTokens struct {
	mu             sync.Mutex
	tok            map[string]*client.APIToken
	secrets        map[string]string
	order          []string
	next           int
	revokeRootOnly bool
	cap            *time.Time // the creating token's expiry, which caps expires_at
	created        int
	revoked        []string
}

func (f *fakeTokens) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if bearer != "unit-token" && bearer != "limited-token" && bearer != "readless-token" {
		http.Error(w, "no", http.StatusUnauthorized)
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	p := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1"), "/")
	parts := strings.Split(p, "/")
	if bearer == "readless-token" && parts[0] == "tokens" && r.Method == http.MethodGet {
		fail(403, "forbidden") // a token without the tokens:read grant
		return
	}
	switch {
	case p == "auth/tokens" && r.Method == http.MethodPost:
		if bearer != "unit-token" {
			fail(403, "forbidden")
			return
		}
		var b struct {
			Name      string   `json:"name"`
			Grants    []string `json:"grants"`
			Root      bool     `json:"root"`
			ExpiresAt *string  `json:"expires_at"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		for _, g := range b.Grants {
			if g == "bogus:grant" {
				fail(400, "validation_failed")
				return
			}
		}
		f.next++
		f.created++
		id := fmt.Sprintf("01JD3Y4C8V9M2Q7W5R6T0XZ%03d", f.next)
		secret := fmt.Sprintf("ldgr_FakeSecretValueNumber%03dAbCdEfGhIjKlMnOpQrSt", f.next)
		t := &client.APIToken{ID: id, Name: b.Name, OwnerID: "01JD3Y4C8V9M2Q7W5R6T0XZROOT", Root: b.Root, Grants: b.Grants, Hint: "ldgr_Fake…QrSt",
			Prefix: "ldgr_Fake", LastChars: "QrSt", Status: client.TokenActive, CreatedAt: now}
		if b.Root {
			t.Grants = []string{}
		}
		exp := b.ExpiresAt
		if f.cap != nil {
			c := f.cap.Format(time.RFC3339)
			if exp == nil {
				exp = &c
			} else if ts, err := time.Parse(time.RFC3339, *exp); err == nil && ts.After(*f.cap) {
				exp = &c
			}
		}
		t.ExpiresAt = exp
		f.tok[id] = t
		f.secrets[id] = secret
		f.order = append(f.order, id)
		var warning *string
		if b.Root {
			w := "A root token holds every grant."
			warning = &w
		}
		send(201, map[string]any{"secret": secret, "token": t, "warning": warning})
	case parts[0] == "tokens" && len(parts) == 1 && r.Method == http.MethodGet:
		items := []*client.APIToken{}
		for _, id := range sorted(append([]string{}, f.order...)) {
			if o := r.URL.Query().Get("owner_id"); o != "" && f.tok[id].OwnerID != o {
				continue
			}
			items = append(items, f.tok[id])
		}
		send(200, map[string]any{"items": items, "next_cursor": nil})
	case parts[0] == "tokens" && len(parts) == 2:
		t, ok := f.tok[parts[1]]
		if !ok {
			fail(404, "not_found")
			return
		}
		switch r.Method {
		case http.MethodGet:
			send(200, t)
		case http.MethodDelete:
			if f.revokeRootOnly && t.Root && bearer != "unit-token" {
				fail(403, client.CodeRootTokenRequired)
				return
			}
			if t.Status != client.TokenRevoked {
				t.Status = client.TokenRevoked
				t.RevokedAt = &now
				f.revoked = append(f.revoked, t.ID)
			}
			w.WriteHeader(204)
		}
	default:
		fail(404, "not_found")
	}
}

func newFakeTokens(t *testing.T) (*fakeTokens, map[string]func() (tfprotov6.ProviderServer, error)) {
	t.Helper()
	requireTerraform(t)
	f := &fakeTokens{tok: map[string]*client.APIToken{}, secrets: map[string]string{}}
	srv := httptest.NewServer(withAPIDocument(f))
	t.Cleanup(srv.Close)
	t.Setenv("LEDGER_ENDPOINT", srv.URL)
	t.Setenv("LEDGER_TOKEN", "unit-token")
	return f, map[string]func() (tfprotov6.ProviderServer, error){"ledger": providerserver.NewProtocol6WithError(New("test")())}
}

func tokenCfg(name, extra string) string {
	return fmt.Sprintf("resource \"ledger_api_token\" \"t\" {\n  name = %q\n%s}\n", name, extra)
}

const grantsCfg = "  grants = [\"commitments:read\", \"organisation:read\"]\n"

// secretOf returns the secret in state, failing the test when there is none.
func secretOnlyInSecretAttr(f *fakeTokens) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		for name, rs := range s.RootModule().Resources {
			f.mu.Lock()
			secret := f.secrets[rs.Primary.ID]
			f.mu.Unlock()
			for k, v := range rs.Primary.Attributes {
				if secret != "" && strings.Contains(v, secret) && (name != "ledger_api_token.t" || k != "secret") {
					return fmt.Errorf("%s.%s holds the token secret", name, k)
				}
			}
		}
		return nil
	}
}

func TestAPITokenSecretIsSensitiveAndDataSourcesHaveNone(t *testing.T) {
	var rs fwresource.SchemaResponse
	NewAPITokenResource().Schema(context.Background(), fwresource.SchemaRequest{}, &rs)
	if a, ok := rs.Schema.Attributes["secret"]; !ok || !a.IsSensitive() || !a.IsComputed() {
		t.Fatal("secret must be a computed, sensitive attribute")
	}
	for _, ds := range []datasource.DataSource{NewAPITokenDataSource(), NewAPITokensDataSource()} {
		var resp datasource.SchemaResponse
		ds.Schema(context.Background(), datasource.SchemaRequest{}, &resp)
		blob := fmt.Sprintf("%v", resp.Schema.Attributes)
		if _, ok := resp.Schema.Attributes["secret"]; ok || strings.Contains(blob, "secret:") {
			t.Fatal("a metadata data source must have no secret attribute")
		}
	}
}

func secretIs(f *fakeTokens, addr string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs := s.RootModule().Resources[addr]
		if rs == nil {
			return fmt.Errorf("no %s", addr)
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if got, want := rs.Primary.Attributes["secret"], f.secrets[rs.Primary.ID]; got == "" || got != want {
			return fmt.Errorf("the state secret does not match the token Ledger issued")
		}
		return nil
	}
}

func TestAPITokenLifecycleAgainstFakeLedger(t *testing.T) {
	f, factories := newFakeTokens(t)
	var firstID string
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		CheckDestroy: func(*terraform.State) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			for _, tk := range f.tok {
				if tk.Status != client.TokenRevoked {
					return fmt.Errorf("destroy must revoke every token, %s is %s", tk.ID, tk.Status)
				}
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: tokenCfg("ci", grantsCfg) + `
data "ledger_api_token" "meta" {
  id = ledger_api_token.t.id
}

data "ledger_api_tokens" "all" {
  depends_on = [ledger_api_token.t]
}
`, Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("ledger_api_token.t", "name", "ci"),
				resource.TestCheckResourceAttr("ledger_api_token.t", "grants.#", "2"),
				resource.TestCheckResourceAttr("ledger_api_token.t", "root", "false"),
				resource.TestCheckResourceAttr("ledger_api_token.t", "status", "active"),
				resource.TestCheckResourceAttrSet("ledger_api_token.t", "hint"),
				resource.TestCheckNoResourceAttr("ledger_api_token.t", "warning"),
				secretIs(f, "ledger_api_token.t"),
				secretOnlyInSecretAttr(f),
				resource.TestCheckResourceAttrPair("data.ledger_api_token.meta", "id", "ledger_api_token.t", "id"),
				resource.TestCheckResourceAttr("data.ledger_api_token.meta", "grants.#", "2"),
				resource.TestCheckNoResourceAttr("data.ledger_api_token.meta", "secret"),
				resource.TestCheckResourceAttr("data.ledger_api_tokens.all", "items.#", "1"),
				func(s *terraform.State) error {
					firstID = s.RootModule().Resources["ledger_api_token.t"].Primary.ID
					return nil
				},
			)},
			// No change: nothing is created again and the secret stays.
			{Config: tokenCfg("ci", grantsCfg), PlanOnly: true},
			// A new name replaces the token: a new identifier and secret, the old token revoked.
			{Config: tokenCfg("ci2", grantsCfg), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("ledger_api_token.t", "name", "ci2"),
				secretIs(f, "ledger_api_token.t"),
				func(s *terraform.State) error {
					id := s.RootModule().Resources["ledger_api_token.t"].Primary.ID
					f.mu.Lock()
					defer f.mu.Unlock()
					if id == firstID || f.tok[firstID].Status != client.TokenRevoked || f.created != 2 {
						return fmt.Errorf("replacement: id %s first %s created %d", id, firstID, f.created)
					}
					return nil
				},
			)},
			// A different grant set replaces it too.
			{Config: tokenCfg("ci2", "  grants = [\"commitments:read\"]\n"), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("ledger_api_token.t", "grants.#", "1"),
				secretIs(f, "ledger_api_token.t"),
				func(*terraform.State) error {
					f.mu.Lock()
					defer f.mu.Unlock()
					if f.created != 3 {
						return fmt.Errorf("created = %d", f.created)
					}
					return nil
				},
			)},
			// A token revoked elsewhere leaves the state on refresh, and the next apply makes a new one.
			{PreConfig: func() {
				f.mu.Lock()
				defer f.mu.Unlock()
				for _, tk := range f.tok {
					tk.Status = client.TokenRevoked
				}
			}, Config: tokenCfg("ci2", "  grants = [\"commitments:read\"]\n"), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("ledger_api_token.t", "status", "active"),
				secretIs(f, "ledger_api_token.t"),
				func(*terraform.State) error {
					f.mu.Lock()
					defer f.mu.Unlock()
					if f.created != 4 {
						return fmt.Errorf("created = %d, want a new token", f.created)
					}
					return nil
				},
			)},
		},
	})
}

func TestAPITokenRootAndExpiry(t *testing.T) {
	f, factories := newFakeTokens(t)
	capAt := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Second)
	f.cap = &capAt
	later := time.Now().UTC().Add(96 * time.Hour).Truncate(time.Second).Format(time.RFC3339)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: tokenCfg("root", "  root = true\n  expires_at = \""+later+"\"\n"), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("ledger_api_token.t", "root", "true"),
				resource.TestCheckNoResourceAttr("ledger_api_token.t", "grants"),
				resource.TestCheckResourceAttr("ledger_api_token.t", "expires_at", later),
				resource.TestCheckResourceAttr("ledger_api_token.t", "effective_expires_at", capAt.Format(time.RFC3339)),
				resource.TestCheckResourceAttrSet("ledger_api_token.t", "warning"),
				secretIs(f, "ledger_api_token.t"),
			)},
			{Config: tokenCfg("root", "  root = true\n  expires_at = \""+later+"\"\n"), PlanOnly: true},
		},
	})
}

func TestAPITokenValidationAndRootTokenPrerequisite(t *testing.T) {
	f, factories := newFakeTokens(t)
	_ = f
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: tokenCfg("x", ""), ExpectError: regexp.MustCompile(`A token needs grants`)},
			{Config: tokenCfg("x", grantsCfg+"  root = true\n"), ExpectError: regexp.MustCompile(`Grants and root are exclusive`)},
			{Config: tokenCfg("x", grantsCfg+"  expires_at = \"tomorrow\"\n"), ExpectError: regexp.MustCompile(`RFC 3339`)},
			{Config: tokenCfg("", grantsCfg), ExpectError: regexp.MustCompile(`(?s)string length must be between 1\s+and 100`)},
			{Config: tokenCfg("x", "  grants = [\"bogus:grant\"]\n"), ExpectError: regexp.MustCompile(`validation_failed`)},
		},
	})
	// A token that is not the root token cannot create tokens: the error says what is needed, and shows no secret.
	t.Setenv("LEDGER_TOKEN", "limited-token")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: tokenCfg("x", grantsCfg), ExpectError: regexp.MustCompile(`(?s)Creating the API token failed.*forbidden.*root\s+token of the root\s+account`)},
		},
	})
}

// Revoking a root token needs the root token too (D34): the hint names it, and nothing is silently forgotten.
func TestAPITokenRevokeNeedsRootTokenHint(t *testing.T) {
	f, factories := newFakeTokens(t)
	f.revokeRootOnly = true
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: tokenCfg("r", "  root = true\n")},
			{PreConfig: func() { t.Setenv("LEDGER_TOKEN", "limited-token") }, Config: tokenCfg("r", "  root = true\n"), Destroy: true,
				ExpectError: regexp.MustCompile(`(?s)Revoking the API token failed.*root_token_required.*root\s+token of the root\s+account`)},
			// Back with the root token, so the test's own cleanup can revoke it.
			{PreConfig: func() { t.Setenv("LEDGER_TOKEN", "unit-token") }, Config: tokenCfg("r", "  root = true\n")},
		},
	})
}

func TestAPITokenDataSourceFilters(t *testing.T) {
	f, factories := newFakeTokens(t)
	now := "2026-01-01T00:00:00Z"
	f.tok["01JD3Y4C8V9M2Q7W5R6T0XZ900"] = &client.APIToken{ID: "01JD3Y4C8V9M2Q7W5R6T0XZ900", Name: "old", OwnerID: "01JD3Y4C8V9M2Q7W5R6T0XZ0W1", Grants: []string{"a:read"}, Status: client.TokenRevoked, CreatedAt: now}
	f.tok["01JD3Y4C8V9M2Q7W5R6T0XZ901"] = &client.APIToken{ID: "01JD3Y4C8V9M2Q7W5R6T0XZ901", Name: "live", OwnerID: "01JD3Y4C8V9M2Q7W5R6T0XZ0W1", Root: true, Status: client.TokenActive, CreatedAt: now}
	f.order = []string{"01JD3Y4C8V9M2Q7W5R6T0XZ900", "01JD3Y4C8V9M2Q7W5R6T0XZ901"}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{{Config: `
data "ledger_api_tokens" "active" {
  statuses = ["active"]
}
data "ledger_api_tokens" "owner" {
  owner_id = "01JD3Y4C8V9M2Q7W5R6T0XZ0W1"
}
data "ledger_api_token" "gone" {
  id = "01JD3Y4C8V9M2Q7W5R6T0XZ999"
}
output "unused" { value = 1 }
`, ExpectError: regexp.MustCompile(`No such API token`)},
			{Config: `
data "ledger_api_tokens" "active" {
  statuses = ["active"]
}
data "ledger_api_tokens" "owner" {
  owner_id = "01JD3Y4C8V9M2Q7W5R6T0XZ0W1"
}
data "ledger_api_token" "live" {
  id = "01JD3Y4C8V9M2Q7W5R6T0XZ901"
}
`, Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.ledger_api_tokens.active", "items.#", "1"),
				resource.TestCheckResourceAttr("data.ledger_api_tokens.active", "items.0.name", "live"),
				resource.TestCheckResourceAttr("data.ledger_api_tokens.owner", "items.#", "2"),
				resource.TestCheckResourceAttr("data.ledger_api_token.live", "root", "true"),
				resource.TestCheckResourceAttr("data.ledger_api_token.live", "grants.#", "0"),
			)},
		},
	})
}

// fakeTokenState builds the state the resource holds: every attribute null except the ones given.
func fakeTokenState(ctx context.Context, t *testing.T, s schema.Schema, set map[string]tftypes.Value) tfsdk.State {
	t.Helper()
	typ, ok := s.Type().TerraformType(ctx).(tftypes.Object)
	if !ok {
		t.Fatal("the resource schema is not an object")
	}
	vals := map[string]tftypes.Value{}
	for name, at := range typ.AttributeTypes {
		vals[name] = tftypes.NewValue(at, nil)
	}
	for name, v := range set {
		vals[name] = v
	}
	return tfsdk.State{Schema: s, Raw: tftypes.NewValue(typ, vals)}
}

// An expired token stays in state, with its secret, and every refresh warns about it (the dead secret must not be silent).
func TestAPITokenExpiredWarnsAndKeepsSecret(t *testing.T) {
	f, _ := newFakeTokens(t)
	ctx := context.Background()
	c, err := client.New(client.Config{Endpoint: os.Getenv("LEDGER_ENDPOINT"), Token: "unit-token"})
	if err != nil {
		t.Fatal(err)
	}
	created, err := c.CreateAPIToken(ctx, client.NewAPIToken{Name: "soon-expired", Grants: []string{"commitments:read"}})
	if err != nil {
		t.Fatal(err)
	}
	id, secret := created.Token.ID, created.Secret.Reveal()
	var rs fwresource.SchemaResponse
	NewAPITokenResource().Schema(ctx, fwresource.SchemaRequest{}, &rs)
	st := fakeTokenState(ctx, t, rs.Schema, map[string]tftypes.Value{
		"id":     tftypes.NewValue(tftypes.String, id),
		"status": tftypes.NewValue(tftypes.String, client.TokenActive),
		"secret": tftypes.NewValue(tftypes.String, secret),
	})
	r := &apiTokenResource{client: c}
	read := func() fwresource.ReadResponse {
		resp := fwresource.ReadResponse{State: st}
		r.Read(ctx, fwresource.ReadRequest{State: st}, &resp)
		return resp
	}

	if resp := read(); resp.Diagnostics.HasError() || resp.Diagnostics.WarningsCount() != 0 {
		t.Fatalf("an active token must read without diagnostics, got %v", resp.Diagnostics)
	}

	f.mu.Lock()
	f.tok[id].Status = client.TokenExpired // Ledger flips the token to expired
	f.mu.Unlock()
	resp := read()
	if resp.Diagnostics.HasError() {
		t.Fatalf("an expired token must not be an error, got %v", resp.Diagnostics)
	}
	warnings := resp.Diagnostics.Warnings()
	if len(warnings) != 1 || warnings[0].Summary() != "API token expired" {
		t.Fatalf("an expired token must warn once with the summary \"API token expired\", got %v", resp.Diagnostics)
	}
	var got apiTokenModel
	if d := resp.State.Get(ctx, &got); d.HasError() {
		t.Fatalf("reading the state: %v", d)
	}
	if got.Status.ValueString() != client.TokenExpired || got.Secret.ValueString() != secret {
		t.Fatalf("an expired token stays in state with its secret: status %q, secret kept %t", got.Status.ValueString(), got.Secret.ValueString() == secret)
	}
}

// A token without tokens:read is refused on refresh and by both data sources: the hint names the grant.
func TestAPITokenReadNeedsTokensReadHint(t *testing.T) {
	f, factories := newFakeTokens(t)
	f.tok["01JD3Y4C8V9M2Q7W5R6T0XZ900"] = &client.APIToken{ID: "01JD3Y4C8V9M2Q7W5R6T0XZ900", Name: "seen", OwnerID: "01JD3Y4C8V9M2Q7W5R6T0XZ0W1", Status: client.TokenActive, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: tokenCfg("r", grantsCfg)},
			{PreConfig: func() { t.Setenv("LEDGER_TOKEN", "readless-token") }, Config: tokenCfg("r", grantsCfg), PlanOnly: true,
				ExpectError: regexp.MustCompile(`(?s)Reading the API token failed.*tokens:read`)},
			{PreConfig: func() { t.Setenv("LEDGER_TOKEN", "readless-token") }, Config: "data \"ledger_api_token\" \"d\" {\n  id = \"01JD3Y4C8V9M2Q7W5R6T0XZ900\"\n}\n",
				ExpectError: regexp.MustCompile(`(?s)Reading the API token failed.*tokens:read`)},
			{Config: "data \"ledger_api_tokens\" \"l\" {}\n", PreConfig: func() { t.Setenv("LEDGER_TOKEN", "readless-token") },
				ExpectError: regexp.MustCompile(`(?s)Listing API tokens failed.*tokens:read`)},
			// Back with the root token, so the test's own cleanup can revoke the token.
			{PreConfig: func() { t.Setenv("LEDGER_TOKEN", "unit-token") }, Config: tokenCfg("r", grantsCfg)},
		},
	})
	_ = f
}
