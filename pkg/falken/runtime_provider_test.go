package falken_test

import (
	"context"

	"github.com/smasonuk/falken-core/pkg/falken"
	falkenruntime "github.com/smasonuk/falken-core/pkg/falken/runtime"
)

var _ falken.RuntimeProvider = externalRuntimeProvider{}
var _ falkenruntime.SandboxRuntime = externalSandboxRuntime{}

type externalRuntimeProvider struct{}

func (externalRuntimeProvider) NewRuntimeAdapters(context.Context, falken.RuntimeAdapterRequest) (falken.RuntimeAdapters, error) {
	return falken.RuntimeAdapters{SandboxRuntime: externalSandboxRuntime{}}, nil
}

type externalSandboxRuntime struct{}

func (externalSandboxRuntime) Start(context.Context) error { return nil }
func (externalSandboxRuntime) Close(context.Context) error { return nil }
func (externalSandboxRuntime) Execute(context.Context, falkenruntime.SandboxCommandRequest) (falkenruntime.SandboxCommandResult, error) {
	return falkenruntime.SandboxCommandResult{Started: true, ExitCode: 0}, nil
}
