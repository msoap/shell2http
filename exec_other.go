//go:build !unix

package main

import "os/exec"

// setProcessGroup - process groups are not supported, on timeout only the shell process is killed
func setProcessGroup(_ *exec.Cmd) {}
