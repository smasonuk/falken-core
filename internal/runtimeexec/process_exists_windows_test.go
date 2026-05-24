//go:build windows

package runtimeexec_test

func processExists(pid int) bool {
	return pid > 0
}
