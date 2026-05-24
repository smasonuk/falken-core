//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package runtimeexec

import (
	"os"
	"os/exec"
	"syscall"
	"time"
)

func startProcess(cmd *exec.Cmd) error {
	return cmd.Start()
}

func startProcessGroup(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd.Start()
}

func stopProcess(process *os.Process) error {
	if process == nil {
		return nil
	}
	pgid, err := syscall.Getpgid(process.Pid)
	if err != nil {
		return process.Kill()
	}
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	time.Sleep(100 * time.Millisecond)
	if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
		return err
	}
	return nil
}
