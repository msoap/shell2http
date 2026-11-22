//go:build !unix

package main

import (
	"os"
	"os/exec"
)

// shutdownSignals - signals for shutdown of the server, running commands with a context are killed
var shutdownSignals = []os.Signal{os.Interrupt}

// setProcessGroup - process groups are not supported, on timeout only the shell process is killed
func setProcessGroup(_ *exec.Cmd) {}
