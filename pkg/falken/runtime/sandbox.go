package runtime

import (
	"context"
	"io"
)

// ProxyConfig configures proxy environment injected into sandboxed commands.
type ProxyConfig struct {
	HTTPProxy  string
	HTTPSProxy string
	NoProxy    string
}

// SandboxCommandRequest describes a command to execute inside a sandbox.
type SandboxCommandRequest struct {
	Command        string
	HostWorkingDir string
	Env            []string
	Stdout         io.Writer
	Stderr         io.Writer
}

// SandboxCommandResult is the sandbox-level execution result.
type SandboxCommandResult struct {
	Started      bool
	ExitCode     int
	StartError   string
	RuntimeError string
	ExitError    string
	CleanupError string
}

// SandboxRuntime is the lifecycle and foreground execution abstraction for
// sandbox adapters. Sandboxes are a container system like docker.
type SandboxRuntime interface {
	Start(context.Context) error
	Execute(context.Context, SandboxCommandRequest) (SandboxCommandResult, error)
	Close(context.Context) error
}
