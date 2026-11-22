package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/aurimasniekis/terraform-provider-ledger/internal/client"
)

const overrideOccID = "01JD3Y4C8V9M2Q7W5R6T0XZ910"

// fakeOverrideOccurrence is an in-memory occurrence with Ledger's override history: a PUT pushes an override, an undo pops
// it and so restores the one it replaced. It checks If-Match (409 version_conflict on a stale version) and bumps the
// version on every write. The test hooks inject what the real server does under concurrency: conflicts, a change by someone
// else between the provider's read and its write, a missing occurrence, and an undo that does not take effect.
type fakeOverrideOccurrence struct {
	mu      sync.Mutex
	version int64
	stack   []client.OverrideState // the last one is active
	n       int

	conflicts int    // the next n PUT or DELETE calls fail with a version conflict
	always    bool   // every PUT or DELETE fails with a version conflict
	race      func() // runs once, on the next write, before it is checked; the write then fails as stale
	missing   bool
	stuck     bool // an undo succeeds but the override stays

	gets, puts, deletes int
}

func (f *fakeOverrideOccurrence) push(status, reason string) {
	f.n++
	f.stack = append(f.stack, client.OverrideState{Status: status, Reason: reason, SetAt: "2026-03-0" + strconv.Itoa(f.n) + "T10:00:00Z", Applied: true})
	f.version++
}

func (f *fakeOverrideOccurrence) occurrence() client.Occurrence {
	o := client.Occurrence{ID: overrideOccID, CommitmentID: "01JD3Y4C8V9M2Q7W5R6T0XZ901", Month: "2026-03", DueDate: "2026-03-01",
		Amount: client.Money{Amount: "850.00", Currency: "EUR"}, Version: f.version}
	o.Payment.Status, o.Payment.DerivedStatus = "overdue", "overdue"
	if len(f.stack) > 0 {
		ov := f.stack[len(f.stack)-1]
		o.Payment.Override = &ov
		o.Payment.Status = ov.Status
	}
	return o
}

func (f *fakeOverrideOccurrence) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/occurrences/")
	if f.missing || !strings.HasPrefix(path, overrideOccID) {
		fail(404, "not_found")
		return
	}
	if r.Method == http.MethodGet {
		f.gets++
		send(200, f.occurrence())
		return
	}
	if r.Method == http.MethodPut {
		f.puts++
	} else {
		f.deletes++
	}
	if f.race != nil {
		race := f.race
		f.race = nil
		race()
	}
	if f.always || f.conflicts > 0 || r.Header.Get("If-Match") != "\""+strconv.FormatInt(f.version, 10)+"\"" {
		if f.conflicts > 0 {
			f.conflicts--
		}
		fail(409, client.CodeVersionConflict)
		return
	}
	if r.Method == http.MethodPut {
		var body struct{ Status, Reason string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.push(body.Status, body.Reason)
	} else if f.stuck {
		f.version++
	} else if len(f.stack) > 0 {
		f.stack = f.stack[:len(f.stack)-1]
		f.version++
	}
	send(200, f.occurrence())
}

type overrideHarness struct {
	t      *testing.T
	f      *fakeOverrideOccurrence
	r      *statusOverrideResource
	schema resource.SchemaResponse
}

func newOverrideHarness(t *testing.T) *overrideHarness {
	t.Helper()
	f := &fakeOverrideOccurrence{version: 1}
	srv := httptest.NewServer(withAPIDocument(f))
	t.Cleanup(srv.Close)
	c, err := client.New(client.Config{Endpoint: srv.URL, Token: "unit-token"})
	if err != nil {
		t.Fatal(err)
	}
	h := &overrideHarness{t: t, f: f, r: &statusOverrideResource{client: c}}
	h.r.Schema(context.Background(), resource.SchemaRequest{}, &h.schema)
	return h
}

func (h *overrideHarness) empty() tfsdk.State {
	return tfsdk.State{Schema: h.schema.Schema, Raw: tftypes.NewValue(h.schema.Schema.Type().TerraformType(context.Background()), nil)}
}

func (h *overrideHarness) state(m statusOverrideModel) tfsdk.State {
	st := h.empty()
	if diags := st.Set(context.Background(), m); diags.HasError() {
		h.t.Fatal(diags)
	}
	return st
}

// wanted is the configuration: computed attributes unknown, as in a plan.
func wanted(status, reason string) statusOverrideModel {
	return statusOverrideModel{
		ID: types.StringUnknown(), OccurrenceID: types.StringValue(overrideOccID), Status: types.StringValue(status), Reason: types.StringValue(reason),
		Applied: types.BoolUnknown(), SetAt: types.StringUnknown(), DerivedStatus: types.StringUnknown(), PaymentStatus: types.StringUnknown(),
	}
}

// seen is what Terraform last read of the active override of the fake.
func (h *overrideHarness) seen() statusOverrideModel {
	h.f.mu.Lock()
	defer h.f.mu.Unlock()
	m, ok := statusOverrideFromAPI(h.f.occurrence())
	if !ok {
		h.t.Fatal("the fake has no override")
	}
	return m
}

func (h *overrideHarness) create(m statusOverrideModel) resource.CreateResponse {
	resp := resource.CreateResponse{State: h.empty()}
	h.r.Create(context.Background(), resource.CreateRequest{Plan: tfsdk.Plan{Schema: h.schema.Schema, Raw: h.state(m).Raw}}, &resp)
	return resp
}

func (h *overrideHarness) update(old, m statusOverrideModel) resource.UpdateResponse {
	resp := resource.UpdateResponse{State: h.state(old)}
	h.r.Update(context.Background(), resource.UpdateRequest{
		Plan: tfsdk.Plan{Schema: h.schema.Schema, Raw: h.state(m).Raw}, State: h.state(old),
	}, &resp)
	return resp
}

func (h *overrideHarness) destroy(old statusOverrideModel) resource.DeleteResponse {
	resp := resource.DeleteResponse{State: h.state(old)}
	h.r.Delete(context.Background(), resource.DeleteRequest{State: h.state(old)}, &resp)
	return resp
}

// errText is every error diagnostic's summary and detail, white space collapsed.
func errText(d diag.Diagnostics) string {
	var parts []string
	for _, e := range d.Errors() {
		parts = append(parts, e.Summary()+" "+e.Detail())
	}
	return strings.Join(strings.Fields(strings.Join(parts, " ")), " ")
}

func (h *overrideHarness) wantError(d diag.Diagnostics, substr string) {
	h.t.Helper()
	if !d.HasError() || !strings.Contains(errText(d), substr) {
		h.t.Fatalf("want an error containing %q, got %q", substr, errText(d))
	}
}

func (h *overrideHarness) wantNoError(d diag.Diagnostics) {
	h.t.Helper()
	if d.HasError() {
		h.t.Fatalf("unexpected error: %s", errText(d))
	}
}

func TestOverrideCreateRetriesVersionConflicts(t *testing.T) {
	h := newOverrideHarness(t)
	h.f.conflicts = 2
	resp := h.create(wanted("unpaid", "by hand"))
	h.wantNoError(resp.Diagnostics)
	if h.f.puts != 3 || len(h.f.stack) != 1 || h.f.stack[0].Reason != "by hand" {
		t.Fatalf("puts = %d, stack = %+v; want 3 PUTs and the override set once", h.f.puts, h.f.stack)
	}
	var got statusOverrideModel
	h.wantNoError(resp.State.Get(context.Background(), &got))
	if got.Status.ValueString() != "unpaid" || got.SetAt.IsNull() {
		t.Fatalf("state = %+v", got)
	}
}

func TestOverrideCreateGivesUpAfterFiveRetries(t *testing.T) {
	h := newOverrideHarness(t)
	h.f.always = true
	resp := h.create(wanted("unpaid", "by hand"))
	h.wantError(resp.Diagnostics, "run terraform plan (refresh) and apply again")
	if h.f.puts != versionRetries+1 {
		t.Fatalf("puts = %d, want the first try and %d retries", h.f.puts, versionRetries)
	}
	if len(h.f.stack) != 0 {
		t.Fatalf("nothing may be written: %+v", h.f.stack)
	}
}

// Someone sets an override between Create's check and its write: the retry must not overwrite it.
func TestOverrideCreateDoesNotOverwriteAnOverrideThatAppearsDuringRetry(t *testing.T) {
	h := newOverrideHarness(t)
	h.f.race = func() { h.f.push("paid", "set in the UI") }
	resp := h.create(wanted("unpaid", "by hand"))
	h.wantError(resp.Diagnostics, "already has an active override (paid)")
	if h.f.puts != 1 || len(h.f.stack) != 1 || h.f.stack[0].Reason != "set in the UI" {
		t.Fatalf("puts = %d, stack = %+v; the override of the UI must stand", h.f.puts, h.f.stack)
	}
}

func TestOverrideUpdateRetriesConflictsWhileTheOverrideIsUnchanged(t *testing.T) {
	h := newOverrideHarness(t)
	h.f.push("unpaid", "first")
	old := h.seen()
	h.f.conflicts = 2
	resp := h.update(old, wanted("paid", "second"))
	h.wantNoError(resp.Diagnostics)
	if h.f.puts != 3 || len(h.f.stack) != 2 || h.f.stack[1].Reason != "second" {
		t.Fatalf("puts = %d, stack = %+v", h.f.puts, h.f.stack)
	}
}

// The occurrence's version moves with payment changes too: a conflict caused by one is retried and the override written.
func TestOverrideUpdateRetriesAConflictCausedByAPaymentChange(t *testing.T) {
	h := newOverrideHarness(t)
	h.f.push("unpaid", "first")
	old := h.seen()
	h.f.race = func() { h.f.version++ }
	h.wantNoError(h.update(old, wanted("paid", "second")).Diagnostics)
	if h.f.puts != 2 || len(h.f.stack) != 2 {
		t.Fatalf("puts = %d, stack = %+v", h.f.puts, h.f.stack)
	}
}

// D17: an override changed in the UI between plan and apply is neither overwritten nor retried over.
func TestOverrideUpdateDoesNotOverwriteAnOutOfBandChange(t *testing.T) {
	h := newOverrideHarness(t)
	h.f.push("unpaid", "first")
	old := h.seen()
	h.f.race = func() { h.f.push("paid", "set in the UI") }
	resp := h.update(old, wanted("unpaid", "second"))
	h.wantError(resp.Diagnostics, "changed outside Terraform since it was last read")
	if h.f.puts != 1 || len(h.f.stack) != 2 || h.f.stack[1].Reason != "set in the UI" {
		t.Fatalf("puts = %d, stack = %+v; the override of the UI must stand and no retry may be sent", h.f.puts, h.f.stack)
	}
}

// The same status and reason set again elsewhere is still a different override: set_at tells.
func TestOverrideUpdateNoticesAnOverrideSetAgainWithTheSameContent(t *testing.T) {
	h := newOverrideHarness(t)
	h.f.push("unpaid", "first")
	old := h.seen()
	h.f.push("unpaid", "first")
	h.wantError(h.update(old, wanted("paid", "second")).Diagnostics, "changed outside Terraform")
	if h.f.puts != 0 {
		t.Fatalf("puts = %d, want none", h.f.puts)
	}
}

func TestOverrideDestroyWalksTheHistoryThroughConflicts(t *testing.T) {
	h := newOverrideHarness(t)
	h.f.push("unpaid", "first")
	h.f.push("paid", "second")
	h.f.push("unpaid", "third")
	old := h.seen()
	h.f.conflicts = 2
	h.wantNoError(h.destroy(old).Diagnostics)
	if len(h.f.stack) != 0 || h.f.deletes != 5 {
		t.Fatalf("deletes = %d, stack = %+v; want 2 conflicts and 3 undo steps, nothing active", h.f.deletes, h.f.stack)
	}
}

func TestOverrideDestroyDoesNotUndoAnOutOfBandChange(t *testing.T) {
	h := newOverrideHarness(t)
	h.f.push("unpaid", "first")
	old := h.seen()
	h.f.race = func() { h.f.push("paid", "set in the UI") }
	h.wantError(h.destroy(old).Diagnostics, "changed outside Terraform since it was last read")
	if h.f.deletes != 1 || len(h.f.stack) != 2 {
		t.Fatalf("deletes = %d, stack = %+v; the override of the UI must stay active", h.f.deletes, h.f.stack)
	}
}

func TestOverrideDestroyExitsWhenNothingIsActiveOrTheOccurrenceIsGone(t *testing.T) {
	h := newOverrideHarness(t)
	h.f.push("unpaid", "first")
	old := h.seen()
	h.f.stack = nil
	h.wantNoError(h.destroy(old).Diagnostics)
	if h.f.deletes != 0 {
		t.Fatalf("deletes = %d, want none", h.f.deletes)
	}
	h.f.missing = true
	h.wantNoError(h.destroy(old).Diagnostics)
}

func TestOverrideDestroyGivesUpAfterFiveConflicts(t *testing.T) {
	h := newOverrideHarness(t)
	h.f.push("unpaid", "first")
	old := h.seen()
	h.f.always = true
	h.wantError(h.destroy(old).Diagnostics, "run terraform plan (refresh) and apply again")
	if h.f.deletes != versionRetries+1 || len(h.f.stack) != 1 {
		t.Fatalf("deletes = %d, stack = %+v", h.f.deletes, h.f.stack)
	}
}

func TestOverrideDestroyStopsAfterMaxUndoSteps(t *testing.T) {
	h := newOverrideHarness(t)
	h.f.push("unpaid", "first")
	old := h.seen()
	h.f.stuck = true
	h.wantError(h.destroy(old).Diagnostics, "still active")
	if h.f.deletes != maxOverrideUndos {
		t.Fatalf("deletes = %d, want %d", h.f.deletes, maxOverrideUndos)
	}
}
