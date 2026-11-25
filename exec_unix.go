//go:build unix

package main

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// shutdownSignals - signals for shutdown of the server, running commands in own process groups are killed
var shutdownSignals = []os.Signal{os.Interrupt, syscall.SIGTERM}

// setProcessGroup - run the command in its own process group, and kill the whole group
// (the shell and everything it started) when the command context is done
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}
