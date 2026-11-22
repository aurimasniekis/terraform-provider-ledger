package ledgertest

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Operation is one HTTP operation of the Management API, written "METHOD /path" as in the OpenAPI document.
type Operation string

var (
	pathLine   = regexp.MustCompile(`^  (/[^\s:]*):\s*$`)
	methodLine = regexp.MustCompile(`^    (get|put|post|delete|patch|head|options|trace):\s*$`)
	indexRow   = regexp.MustCompile("^\\| `([A-Z]+ /[^`]*)` \\| (.+) \\| (done|follow-up|exception)( [^|]*)? \\|$")
)

// SpecOperations lists the operations of an OpenAPI document written the way go-ledger writes it (block style, paths at
// two spaces of indentation, methods at four). It returns the operations and the number of distinct paths. It reads only
// the paths section and is not a general YAML parser: an unexpected layout is an error, not a silent miss.
func SpecOperations(doc string) (ops []Operation, paths int, err error) {
	in := false
	cur := ""
	seenPath := map[string]bool{}
	for _, line := range strings.Split(doc, "\n") {
		if !in {
			in = line == "paths:"
			continue
		}
		if line != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "#") {
			break // the next top-level key ends paths
		}
		if m := pathLine.FindStringSubmatch(line); m != nil {
			cur = m[1]
			if seenPath[cur] {
				return nil, 0, fmt.Errorf("path %s appears twice", cur)
			}
			seenPath[cur] = true
			continue
		}
		if m := methodLine.FindStringSubmatch(line); m != nil {
			if cur == "" {
				return nil, 0, fmt.Errorf("method %s before any path", m[1])
			}
			ops = append(ops, Operation(strings.ToUpper(m[1])+" "+cur))
		}
	}
	if len(ops) == 0 {
		return nil, 0, fmt.Errorf("no operations found: is this an OpenAPI document with a paths section?")
	}
	return ops, len(seenPath), nil
}

// InventoryIndex is what the coverage inventory (ADR-0005) states about the API it was derived from.
type InventoryIndex struct {
	Commit     string
	Operations int
	Paths      int
	Rows       map[Operation]string // operation to status word
}

var (
	commitLine = regexp.MustCompile("(?m)^Derived against go-ledger `work` at `([0-9a-f]{40})`: \\*\\*(\\d+) operations on (\\d+) paths\\*\\*")
)

// ParseInventory reads the operation index of the ADR text.
func ParseInventory(adr string) (InventoryIndex, error) {
	idx := InventoryIndex{Rows: map[Operation]string{}}
	m := commitLine.FindStringSubmatch(adr)
	if m == nil {
		return idx, fmt.Errorf("the ADR has no 'Derived against go-ledger `work` at `<commit>`: **N operations on M paths**' line")
	}
	idx.Commit = m[1]
	_, _ = fmt.Sscan(m[2], &idx.Operations)
	_, _ = fmt.Sscan(m[3], &idx.Paths)
	// Only the operation index section counts; the mapping tables above mention operations in prose.
	start := strings.Index(adr, "\n## Operation index\n")
	if start < 0 {
		return idx, fmt.Errorf("the ADR has no '## Operation index' section")
	}
	section := adr[start+1:]
	if end := strings.Index(section[3:], "\n## "); end >= 0 {
		section = section[:end+3]
	}
	for _, line := range strings.Split(section, "\n") {
		r := indexRow.FindStringSubmatch(line)
		if r == nil {
			continue
		}
		op := Operation(r[1])
		if _, dup := idx.Rows[op]; dup {
			return idx, fmt.Errorf("operation %s is listed twice", op)
		}
		idx.Rows[op] = r[3]
	}
	return idx, nil
}

// Compare reports what differs between the inventory's index and the operations the API serves. An empty result means
// the inventory covers exactly the served operations and states the right counts.
func (idx InventoryIndex) Compare(ops []Operation, paths int) []string {
	var out []string
	served := map[Operation]bool{}
	for _, op := range ops {
		served[op] = true
		if _, ok := idx.Rows[op]; !ok {
			out = append(out, "not in the inventory: "+string(op))
		}
	}
	for op := range idx.Rows {
		if !served[op] {
			out = append(out, "in the inventory but not served: "+string(op))
		}
	}
	if idx.Operations != len(ops) || idx.Paths != paths {
		out = append(out, fmt.Sprintf("the inventory states %d operations on %d paths, the document has %d on %d", idx.Operations, idx.Paths, len(ops), paths))
	}
	sort.Strings(out)
	return out
}
