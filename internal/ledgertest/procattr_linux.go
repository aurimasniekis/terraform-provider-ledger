//go:build linux

package ledgertest

import (
	"os/exec"
	"syscall"
)

// dieWithParent makes the kernel kill the child when the thread that started it exits, so a killed or crashed test
// binary cannot leave a ledger server running. Other platforms rely on the test cleanup only.
func dieWithParent(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}
