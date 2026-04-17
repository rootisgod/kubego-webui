//go:build !windows

package api

import (
	"os/exec"
	"syscall"
)

// setProcGroup puts the child in its own process group so a later
// killProcGroup can take down ansible and any grandchild SSH processes
// with a single signal. Keep together with the matching windows stub.
func setProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProcGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
