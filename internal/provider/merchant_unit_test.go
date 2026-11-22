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

// fakeMerchants is an in-memory stand-in for the merchant endpoints. It enforces If-Match on every change (409
// version_conflict on a stale version), keeps the stored spelling of an alias whose key the merchant already has (as
// Ledger does), and lets a test run a "human" change in the middle of the first write request, which is after the
// provider read the merchant: that is how a change between read and write is simulated offline.
type fakeMerchants struct {
	mu       sync.Mutex
	m        map[string]*client.Merchant
	next     int
	inUse    bool
	race     func(*client.Merchant) // runs once, at the start of the next version-checked change request
	accepted int                    // version-checked changes that were applied (create and aliases excluded)
}

func (f *fakeMerchants) id() string {
	f.next++
	return fmt.Sprintf("01JD3Y4C8V9M2Q7W5R6T0XZ%03d", f.next)
}

func (f *fakeMerchants) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	switch {
	case p == "/merchants" && r.Method == http.MethodPost:
		var b struct {
			DisplayName string `json:"display_name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		m := &client.Merchant{ID: f.id(), DisplayName: b.DisplayName, Version: 1, CreatedAt: now, UpdatedAt: now}
		m.Aliases = []client.MerchantAlias{{ID: f.id(), Name: b.DisplayName, Key: aliasKey(b.DisplayName), Origin: "display_name", CreatedAt: now}}
		f.m[m.ID] = m
		send(201, m)
		return
	case len(parts) >= 3 && parts[0] == "organisation" && parts[1] == "merchant" && r.Method == http.MethodPut:
		if m := f.m[parts[2]]; m == nil {
			fail(404, "not_found")
		} else if m.Archived {
			fail(409, client.CodeMerchantArchived)
		} else {
			send(200, map[string]any{})
		}
		return
	case len(parts) >= 2 && parts[0] == "merchants":
	default:
		fail(404, "not_found")
		return
	}
	m := f.m[parts[1]]
	if m == nil {
		fail(404, "not_found")
		return
	}
	if r.Method == http.MethodGet {
		send(200, m)
		return
	}
	isAlias := len(parts) >= 3 && parts[2] == "aliases"
	if !isAlias {
		if f.race != nil { // a person acts between the provider's read and this write
			race := f.race
			f.race = nil
			race(m)
		}
		if r.Header.Get("If-Match") != fmt.Sprintf("\"%d\"", m.Version) {
			fail(409, client.CodeVersionConflict)
			return
		}
	}
	bump := func() { m.Version++; m.UpdatedAt = now }
	switch {
	case isAlias && r.Method == http.MethodPost:
		var b struct {
			Name string `json:"name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		key := aliasKey(b.Name)
		for _, a := range m.Aliases {
			if a.Key == key {
				send(200, m) // already there: no change, the stored spelling stays
				return
			}
		}
		m.Aliases = append(m.Aliases, client.MerchantAlias{ID: f.id(), Name: b.Name, Key: key, Origin: "user", CreatedAt: now})
		bump()
		send(200, m)
	case isAlias && r.Method == http.MethodDelete:
		for i, a := range m.Aliases {
			if a.ID == parts[3] {
				m.Aliases = append(m.Aliases[:i], m.Aliases[i+1:]...)
				bump()
				break
			}
		}
		send(200, m)
	case r.Method == http.MethodPatch:
		var b struct {
			DisplayName string `json:"display_name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		m.DisplayName = b.DisplayName
		f.accepted++
		bump()
		send(200, m)
	case len(parts) == 3 && parts[2] == "archive":
		ts := now
		m.Archived, m.ArchivedAt = true, &ts
		f.accepted++
		bump()
		send(200, m)
	case len(parts) == 3 && parts[2] == "unarchive":
		m.Archived, m.ArchivedAt = false, nil
		f.accepted++
		bump()
		send(200, m)
	case r.Method == http.MethodDelete:
		if f.inUse {
			fail(409, client.CodeMerchantInUse)
			return
		}
		f.accepted++
		delete(f.m, m.ID)
		w.WriteHeader(204)
	default:
		fail(404, "not_found")
	}
}

func (f *fakeMerchants) only() *client.Merchant {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.m {
		return m
	}
	return nil
}

func newFakeMerchants(t *testing.T) (*fakeMerchants, map[string]func() (tfprotov6.ProviderServer, error)) {
	t.Helper()
	requireTerraform(t)
	f := &fakeMerchants{m: map[string]*client.Merchant{}}
	srv := httptest.NewServer(withAPIDocument(f))
	t.Cleanup(srv.Close)
	t.Setenv("LEDGER_ENDPOINT", srv.URL)
	t.Setenv("LEDGER_TOKEN", "unit-token")
	return f, map[string]func() (tfprotov6.ProviderServer, error){"ledger": providerserver.NewProtocol6WithError(New("test")())}
}

func merchantCfg(name string, archived bool, extra string) string {
	return fmt.Sprintf("resource \"ledger_merchant\" \"shop\" {\n  display_name = %q\n  archived     = %t\n}\n%s", name, archived, extra)
}

func aliasCfg(name string) string {
	return fmt.Sprintf("resource \"ledger_merchant_alias\" \"a\" {\n  merchant_id = ledger_merchant.shop.id\n  name        = %q\n}\n", name)
}

func TestMerchantAliasMatchesByKey(t *testing.T) {
	f, factories := newFakeMerchants(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			// A case variant of the display-name alias: Ledger keeps "Lidl", so the provider refuses and names it.
			{Config: merchantCfg("Lidl", false, aliasCfg("LIDL")), ExpectError: regexp.MustCompile(`already has this alias under another\s+spelling`)},
			{PreConfig: func() {
				if m := f.only(); m == nil || len(m.Aliases) != 1 {
					t.Errorf("a refused alias must not add one: %+v", m)
				}
			}, Config: merchantCfg("Lidl", false, aliasCfg("Lidl")), Check: resource.ComposeAggregateTestCheckFunc(
				// The exact display name is taken over: same alias, key as Ledger matches it.
				resource.TestCheckResourceAttr("ledger_merchant_alias.a", "key", "lidl"),
				resource.TestCheckResourceAttr("ledger_merchant_alias.a", "origin", "display_name"),
				func(*terraform.State) error {
					if m := f.only(); len(m.Aliases) != 1 {
						return fmt.Errorf("expected the display-name alias to be reused, got %d aliases", len(m.Aliases))
					}
					return nil
				},
			)},
		},
	})
}

// Note: HCL (cty) normalises every string in a configuration to NFC before the provider sees it, so a non-NFC name can
// only reach the validator from other clients; the validator is covered directly (TestMerchantNameValidatorRefusesNonNFC).

// A change made by a person between the provider's read and its write is a conflict: nothing is written.
func TestMerchantVersionRetryRefusals(t *testing.T) {
	humanRename := func(m *client.Merchant) { m.DisplayName, m.Version = "Renamed by a person", m.Version+1 }
	humanArchive := func(m *client.Merchant) { m.Archived, m.Version = true, m.Version+1 }
	aliasOnly := func(m *client.Merchant) { m.Version++ }
	cases := []struct {
		name       string
		race       func(*client.Merchant)
		next       string // the configuration applied after the race is armed
		final      string // the configuration that matches the real state afterwards (a refreshing apply, so the test can destroy)
		wantErr    bool
		wantWrites int
		check      func(*client.Merchant) error
	}{
		{"human rename, then archive", humanRename, merchantCfg("Shop", true, ""), merchantCfg("Renamed by a person", false, ""), true, 0,
			func(m *client.Merchant) error {
				if m.Archived || m.DisplayName != "Renamed by a person" {
					return fmt.Errorf("the person's rename must stand and nothing archived: %+v", m)
				}
				return nil
			}},
		{"human archive, then rename", humanArchive, merchantCfg("Shop 2", false, ""), merchantCfg("Shop", true, ""), true, 0,
			func(m *client.Merchant) error {
				if !m.Archived || m.DisplayName != "Shop" {
					return fmt.Errorf("the person's archive must stand and nothing renamed: %+v", m)
				}
				return nil
			}},
		// A version moved only by an alias change is retried with the current version and succeeds.
		{"version moved by an alias only, then rename", aliasOnly, merchantCfg("Shop 2", false, ""), merchantCfg("Shop 2", false, ""), false, 1,
			func(m *client.Merchant) error {
				if m.DisplayName != "Shop 2" {
					return fmt.Errorf("the rename should have been applied: %+v", m)
				}
				return nil
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, factories := newFakeMerchants(t)
			steps := []resource.TestStep{
				{Config: merchantCfg("Shop", false, "")},
				{PreConfig: func() { f.mu.Lock(); f.race = tc.race; f.mu.Unlock() }, Config: tc.next},
			}
			if tc.wantErr {
				steps[1].ExpectError = regexp.MustCompile(`changed outside\s+Terraform`)
			}
			steps = append(steps, resource.TestStep{
				PreConfig: func() {
					f.mu.Lock()
					defer f.mu.Unlock()
					if f.accepted != tc.wantWrites {
						t.Errorf("accepted changes = %d, want %d", f.accepted, tc.wantWrites)
					}
					for _, m := range f.m {
						if err := tc.check(m); err != nil {
							t.Error(err)
						}
					}
					f.race = nil
				},
				Config: tc.final,
			})
			resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: factories, Steps: steps})
		})
	}
}

func TestMerchantDeleteRefusedAfterHumanChange(t *testing.T) {
	f, factories := newFakeMerchants(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: merchantCfg("Shop", false, "")},
			{PreConfig: func() {
				f.mu.Lock()
				f.race = func(m *client.Merchant) { m.DisplayName, m.Version = "Renamed by a person", m.Version+1 }
				f.mu.Unlock()
			}, Config: merchantCfg("Shop", false, ""), Destroy: true, ExpectError: regexp.MustCompile(`changed outside\s+Terraform`)},
			{PreConfig: func() {
				f.mu.Lock()
				defer f.mu.Unlock()
				if len(f.m) != 1 || f.accepted != 0 {
					t.Errorf("the merchant must still exist and nothing be written: merchants=%d accepted=%d", len(f.m), f.accepted)
				}
			}, Config: merchantCfg("Renamed by a person", false, "")},
		},
	})
}

func TestMerchantDeleteMergedExplainsMerge(t *testing.T) {
	f, factories := newFakeMerchants(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: merchantCfg("Shop", false, "")},
			{PreConfig: func() {
				f.mu.Lock()
				defer f.mu.Unlock()
				target := "01JD3Y4C8V9M2Q7W5R6T0XZ999"
				for _, m := range f.m {
					m.MergedInto, m.Archived, m.Version = &target, true, m.Version+1
				}
				f.inUse = true
			}, Config: merchantCfg("Shop", true, ""), Destroy: true, ExpectError: regexp.MustCompile(`The merchant was merged and cannot be\s+deleted`)},
			{PreConfig: func() { f.mu.Lock(); f.inUse = false; f.mu.Unlock() }, Config: merchantCfg("Shop", true, "")},
		},
	})
}

func TestAssignmentToArchivedMerchantSaysUnarchive(t *testing.T) {
	_, factories := newFakeMerchants(t)
	assign := `
resource "ledger_category_assignment" "x" {
  object_type = "merchant"
  object_id   = ledger_merchant.shop.id
  category_id = "01JD3Y4C8V9M2Q7W5R6T0XZ998"
}
`
	label := `
resource "ledger_label_assignment" "x" {
  object_type = "merchant"
  object_id   = ledger_merchant.shop.id
  label_id    = "01JD3Y4C8V9M2Q7W5R6T0XZ997"
}
`
	re := regexp.MustCompile(`The merchant is archived`)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: merchantCfg("Shop", true, assign), ExpectError: re},
			{Config: merchantCfg("Shop", true, label), ExpectError: re},
		},
	})
}
