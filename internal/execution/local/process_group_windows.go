//go:build windows

package local

import "os/exec"

func configureCommandProcess(*exec.Cmd) {}

func terminateCommandProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
