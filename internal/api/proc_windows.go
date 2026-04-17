//go:build windows

package api

import "os/exec"

func setProcGroup(cmd *exec.Cmd) {
	// No process group support on Windows; the runner relies on Process.Kill
	// which terminates the child but not grandchildren.
}

func killProcGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		cmd.Process.Kill()
	}
}
