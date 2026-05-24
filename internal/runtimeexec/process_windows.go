//go:build windows

package runtimeexec

import (
	"os"
	"os/exec"
)

func startProcess(cmd *exec.Cmd) error {
	return cmd.Start()
}

func startProcessGroup(cmd *exec.Cmd) error {
	return cmd.Start()
}

func stopProcess(process *os.Process) error {
	if process == nil {
		return nil
	}
	return process.Kill()
}
