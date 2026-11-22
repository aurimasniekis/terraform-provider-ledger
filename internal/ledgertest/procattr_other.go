//go:build !linux

package ledgertest

import "os/exec"

// dieWithParent is a no-op where Pdeathsig does not exist; the test cleanup stops the server.
func dieWithParent(*exec.Cmd) {}
