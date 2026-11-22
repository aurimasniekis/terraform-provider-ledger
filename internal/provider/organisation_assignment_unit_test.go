package provider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/aurimasniekis/terraform-provider-ledger/internal/client"
)

const (
	testObjectID   = "01JD3Y4C8V9M2Q7W5R6T0XZ100"
	testCategoryID = "01JD3Y4C8V9M2Q7W5R6T0XZ200"
	testLabelID    = "01JD3Y4C8V9M2Q7W5R6T0XZ300"
)

// fakeAssignments serves one object's organisation: the category decision and the labels, each with an origin, so
// the assignment resources' Read can be shown to drop anything that is not the person's own direct decision.
type fakeAssignments struct {
	mu       sync.Mutex
	category *client.CategoryAssignment
	labels   []client.LabelAssignment
}

func (f *fakeAssignments) view() client.ObjectAssignments {
	return client.ObjectAssignments{ObjectType: "commitment", ObjectID: testObjectID, Category: f.category, Labels: f.labels}
}

func (f *fakeAssignments) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer unit-token" {
		http.Error(w, "no", http.StatusUnauthorized)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/organisation/commitment/"+testObjectID)
	if rest == r.URL.Path {
		http.NotFound(w, r)
		return
	}
	switch {
	case r.Method == http.MethodPut && rest == "/category":
		var b struct {
			CategoryID *string `json:"category_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		ca := &client.CategoryAssignment{Origin: "user"}
		if b.CategoryID != nil {
			ca.Category = &client.Definition{ID: *b.CategoryID, Kind: "category", Name: "Housing", Version: 1}
		}
		f.category = ca
	case r.Method == http.MethodDelete && rest == "/category":
		f.category = nil
		w.WriteHeader(http.StatusNoContent)
		return
	case r.Method == http.MethodPut && strings.HasPrefix(rest, "/labels/"):
		f.labels = []client.LabelAssignment{{Label: client.Definition{ID: strings.TrimPrefix(rest, "/labels/"), Kind: "label", Name: "L", Version: 1}, Origin: "user"}}
	case r.Method == http.MethodDelete && strings.HasPrefix(rest, "/labels/"):
		f.labels = nil
		w.WriteHeader(http.StatusNoContent)
		return
	case r.Method != http.MethodGet:
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(f.view())
}

func (f *fakeAssignments) set(fn func()) { f.mu.Lock(); defer f.mu.Unlock(); fn() }

func assignmentTestCase(t *testing.T, f *fakeAssignments, steps []resource.TestStep) {
	t.Helper()
	requireTerraform(t)
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	t.Setenv("LEDGER_ENDPOINT", srv.URL)
	t.Setenv("LEDGER_TOKEN", "unit-token")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){"ledger": providerserver.NewProtocol6WithError(New("test")())},
		Steps:                    steps,
	})
}

// TestAssignmentReadDropsAutomaticAndInheritedRows: a rule or model row, or one inherited from a pending transaction,
// is not the person's decision, so Read removes the resource from state and the plan shows it to be created.
func TestAssignmentReadDropsAutomaticAndInheritedRows(t *testing.T) {
	f := &fakeAssignments{}
	cfg := `
resource "ledger_category_assignment" "c" {
  object_type = "commitment"
  object_id   = "` + testObjectID + `"
  category_id = "` + testCategoryID + `"
}

resource "ledger_label_assignment" "l" {
  object_type = "commitment"
  object_id   = "` + testObjectID + `"
  label_id    = "` + testLabelID + `"
}
`
	drift := func(fn func()) resource.TestStep {
		return resource.TestStep{PreConfig: func() { f.set(fn) }, Config: cfg, PlanOnly: true, ExpectNonEmptyPlan: true}
	}
	assignmentTestCase(t, f, []resource.TestStep{
		{Config: cfg},
		{Config: cfg, PlanOnly: true},
		drift(func() { f.category.Origin = "rule" }),
		{Config: cfg}, // applying hands the decision back to the person
		drift(func() { f.category.Origin = "model" }),
		{Config: cfg},
		drift(func() { f.category.Inherited = true }),
		{Config: cfg},
		drift(func() { f.labels[0].Origin = "rule" }),
		{Config: cfg},
		drift(func() { f.labels[0].Origin = "model" }),
		{Config: cfg},
		drift(func() { f.labels[0].Inherited = true }),
		{Config: cfg},
		drift(func() { f.category = nil }),
		{Config: cfg},
	})
}

// TestCategoryAssignmentNoCategory: omitting category_id records that the person chose none. Ledger answers with a
// user-origin row whose category is null; that is the person's decision and plans nothing, while an automatic row
// with no category is not.
func TestCategoryAssignmentNoCategory(t *testing.T) {
	f := &fakeAssignments{}
	cfg := `
resource "ledger_category_assignment" "none" {
  object_type = "commitment"
  object_id   = "` + testObjectID + `"
}
`
	assignmentTestCase(t, f, []resource.TestStep{
		{Config: cfg, Check: resource.TestCheckNoResourceAttr("ledger_category_assignment.none", "category_id")},
		{Config: cfg, PlanOnly: true},
		{PreConfig: func() { f.set(func() { f.category.Origin = "rule" }) }, Config: cfg, PlanOnly: true, ExpectNonEmptyPlan: true},
		{Config: cfg},
		// The person's decision was reset outside Terraform: there is no category row at all.
		{PreConfig: func() { f.set(func() { f.category = nil }) }, Config: cfg, PlanOnly: true, ExpectNonEmptyPlan: true},
		{Config: cfg},
	})
}
