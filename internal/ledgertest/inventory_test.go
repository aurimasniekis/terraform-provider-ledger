package ledgertest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixtureSpec = `openapi: 3.1.1
info:
  version: 1.0.0
paths:
  /me:
    get:
      operationId: getMe
  /users/{id}:
    get:
      operationId: getUser
    patch:
      operationId: updateUser
components:
  schemas:
    X:
      type: object
`

func fixtureADR(commit, ops, paths string, rows ...string) string {
	return "\n## Operation index\n\nDerived against go-ledger `work` at `" + commit + "`: **" + ops + " operations on " + paths + " paths**\n\n" + strings.Join(rows, "\n") + "\n"
}

const commit40 = "0123456789abcdef0123456789abcdef01234567"

func TestSpecOperationsReadsOnlyThePathsSection(t *testing.T) {
	ops, paths, err := SpecOperations(fixtureSpec)
	if err != nil || paths != 2 || len(ops) != 3 {
		t.Fatalf("got %v, %d paths, %v", ops, paths, err)
	}
	if _, _, err := SpecOperations("openapi: 3.1.1\n"); err == nil {
		t.Fatal("a document without paths was accepted")
	}
}

func TestInventoryCompare(t *testing.T) {
	ops, paths, _ := SpecOperations(fixtureSpec)
	good := fixtureADR(commit40, "3", "2",
		"| `GET /me` | data source | follow-up F5 |",
		"| `GET /users/{id}` | resource | done |",
		"| `PATCH /users/{id}` | resource | exception |")
	idx, err := ParseInventory(good)
	if err != nil {
		t.Fatal(err)
	}
	if got := idx.Compare(ops, paths); len(got) != 0 {
		t.Fatalf("a complete inventory differs: %v", got)
	}
	missing := fixtureADR(commit40, "3", "2", "| `GET /me` | data source | done |", "| `GET /users/{id}` | resource | done |")
	idx, _ = ParseInventory(missing)
	if got := strings.Join(idx.Compare(ops, paths), ";"); !strings.Contains(got, "not in the inventory: PATCH /users/{id}") {
		t.Fatalf("a missing operation was not reported: %q", got)
	}
	extra := fixtureADR(commit40, "4", "3",
		"| `GET /me` | x | done |", "| `GET /users/{id}` | x | done |", "| `PATCH /users/{id}` | x | done |", "| `DELETE /gone` | x | done |")
	idx, _ = ParseInventory(extra)
	if got := strings.Join(idx.Compare(ops, paths), ";"); !strings.Contains(got, "in the inventory but not served: DELETE /gone") || !strings.Contains(got, "states 4 operations") {
		t.Fatalf("an extra operation or the wrong counts were not reported: %q", got)
	}
}

func TestParseInventoryRejectsDuplicatesAndMissingHeader(t *testing.T) {
	if _, err := ParseInventory(fixtureADR(commit40, "1", "1", "| `GET /me` | x | done |", "| `GET /me` | y | done |")); err == nil {
		t.Fatal("a duplicate row was accepted")
	}
	if _, err := ParseInventory("| `GET /me` | x | done |\n"); err == nil {
		t.Fatal("an inventory without a commit line was accepted")
	}
}

// TestCoverageInventoryADR checks the committed ADR on its own (no go-ledger needed): a well-formed index whose row count
// is the stated operation count. TestCoverageInventoryAgainstLedger compares it with the real API document.
func TestCoverageInventoryADR(t *testing.T) {
	idx := committedInventory(t)
	if len(idx.Rows) != idx.Operations {
		t.Fatalf("the index has %d rows, the ADR states %d operations", len(idx.Rows), idx.Operations)
	}
}

// TestCoverageInventoryAgainstLedger needs LEDGER_INVENTORY_SOURCE: a go-ledger checkout at the commit the ADR names
// ("make inventory-check"). It fails when the checkout is at another commit, so a pass always means the stated commit.
func TestCoverageInventoryAgainstLedger(t *testing.T) {
	src := os.Getenv("LEDGER_INVENTORY_SOURCE")
	if src == "" {
		t.Skip("set LEDGER_INVENTORY_SOURCE to a go-ledger checkout at the commit named in docs/decisions/0005-coverage-inventory.md (make inventory-check)")
	}
	idx := committedInventory(t)
	if err := verifyCommit(src, idx.Commit); err != nil {
		t.Fatal(err)
	}
	doc, err := os.ReadFile(filepath.Join(src, "internal", "httpapi", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	ops, paths, err := SpecOperations(string(doc))
	if err != nil {
		t.Fatal(err)
	}
	if diff := idx.Compare(ops, paths); len(diff) != 0 {
		t.Fatalf("ADR-0005 does not match go-ledger %s:\n  %s", idx.Commit, strings.Join(diff, "\n  "))
	}
}

func committedInventory(t *testing.T) InventoryIndex {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "decisions", "0005-coverage-inventory.md"))
	if err != nil {
		t.Fatal(err)
	}
	idx, err := ParseInventory(string(b))
	if err != nil {
		t.Fatal(err)
	}
	return idx
}
