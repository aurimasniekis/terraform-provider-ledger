package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/aurimasniekis/terraform-provider-ledger/internal/client"
)

// fakeLedger is a tiny in-memory stand-in for the label endpoints of the Management API. It enforces the version
// rules the provider relies on (If-Match on change, 409 on a stale one) so the provider's lifecycle can be exercised
// offline. The real server is covered by the acceptance tests (internal/acceptance).
type fakeLedger struct {
	mu     sync.Mutex
	labels map[string]*client.Definition
	next   int
}

func (f *fakeLedger) id() string { f.next++; return fmt.Sprintf("01JD3Y4C8V9M2Q7W5R6T0XZ%03d", f.next) }

func (f *fakeLedger) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer unit-token" {
		http.Error(w, "no", http.StatusUnauthorized)
		return
	}
	send := func(code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	errOut := func(code int, c string) { send(code, map[string]any{"code": c, "message": c, "request_id": "R"}) }
	now := time.Now().UTC().Format(time.RFC3339)
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/labels")
	if path == "" {
		switch r.Method {
		case http.MethodPost:
			var b struct {
				Name string `json:"name"`
			}
			_ = json.NewDecoder(r.Body).Decode(&b)
			d := &client.Definition{ID: f.id(), Kind: "label", Name: b.Name, Version: 1, CreatedAt: now, UpdatedAt: now}
			f.labels[d.ID] = d
			send(201, d)
		case http.MethodGet:
			items := []*client.Definition{}
			for _, d := range f.labels {
				items = append(items, d)
			}
			send(200, map[string]any{"items": items, "next_cursor": nil})
		}
		return
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	d, ok := f.labels[parts[0]]
	if !ok {
		errOut(404, "not_found")
		return
	}
	if r.Method == http.MethodGet {
		send(200, d)
		return
	}
	if r.Header.Get("If-Match") != fmt.Sprintf("\"%d\"", d.Version) {
		send(409, map[string]any{"code": "version_conflict", "message": "stale", "request_id": "R", "reconciliation": map[string]any{"current_version": d.Version}})
		return
	}
	switch {
	case r.Method == http.MethodDelete:
		delete(f.labels, d.ID)
		send(200, map[string]int{"reassigned": 0})
		return
	case r.Method == http.MethodPatch:
		var b struct {
			Name string `json:"name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		d.Name = b.Name
	case len(parts) == 2 && parts[1] == "archive":
		d.Archived, d.ArchivedAt = true, &now
	case len(parts) == 2 && parts[1] == "unarchive":
		d.Archived, d.ArchivedAt = false, nil
	}
	d.Version++
	d.UpdatedAt = now
	send(200, d)
}

func requireTerraform(t *testing.T) {
	t.Helper()
	if os.Getenv("TF_ACC_TERRAFORM_PATH") != "" {
		return
	}
	if p, err := exec.LookPath("terraform"); err == nil {
		t.Setenv("TF_ACC_TERRAFORM_PATH", p)
		return
	}
	t.Skip("no terraform CLI on PATH (and TF_ACC_TERRAFORM_PATH is unset): skipping the provider lifecycle test")
}

func TestLabelLifecycleAgainstFakeLedger(t *testing.T) {
	requireTerraform(t)
	f := &fakeLedger{labels: map[string]*client.Definition{}}
	srv := httptest.NewServer(withAPIDocument(f))
	t.Cleanup(srv.Close)
	t.Setenv("LEDGER_ENDPOINT", srv.URL)
	t.Setenv("LEDGER_TOKEN", "unit-token")
	cfg := func(name string, archived bool) string {
		return fmt.Sprintf("resource \"ledger_label\" \"x\" {\n  name     = %q\n  archived = %t\n}\n", name, archived)
	}
	withData := cfg("Reviewed", false) + "\ndata \"ledger_label\" \"by_name\" {\n  name       = \"reviewed\"\n  depends_on = [ledger_label.x]\n}\n"
	rename := func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, d := range f.labels {
			d.Name, d.Version = "Changed elsewhere", d.Version+1
		}
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){"ledger": providerserver.NewProtocol6WithError(New("test")())},
		Steps: []resource.TestStep{
			{Config: withData, Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("ledger_label.x", "version", "1"),
				resource.TestCheckResourceAttrPair("data.ledger_label.by_name", "id", "ledger_label.x", "id"),
				resource.TestCheckResourceAttr("data.ledger_label.by_name", "name", "reviewed"),
			)},
			{Config: cfg("Reviewed", false), PlanOnly: true},
			{ResourceName: "ledger_label.x", ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"reassign_to"}},
			{Config: cfg("Checked", false), Check: resource.TestCheckResourceAttr("ledger_label.x", "name", "Checked")},
			{Config: cfg("Checked", true), Check: resource.TestCheckResourceAttr("ledger_label.x", "archived", "true")},
			{Config: cfg("Checked", false), Check: resource.TestCheckResourceAttr("ledger_label.x", "archived", "false")},
			// A change made outside Terraform shows in the plan, and applying brings the configuration back.
			{PreConfig: rename, Config: cfg("Checked", false), PlanOnly: true, ExpectNonEmptyPlan: true},
			{Config: cfg("Checked", false), Check: resource.TestCheckResourceAttr("ledger_label.x", "name", "Checked")},
			// A label deleted outside Terraform is planned for creation again.
			{PreConfig: func() { f.mu.Lock(); f.labels = map[string]*client.Definition{}; f.mu.Unlock() }, Config: cfg("Checked", false), PlanOnly: true, ExpectNonEmptyPlan: true},
			{Config: cfg("Checked", false)},
		},
	})
}
