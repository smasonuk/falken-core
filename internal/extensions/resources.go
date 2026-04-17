package extensions

import (
	"context"
	"errors"
	"sync"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// ResourceSet owns the Wazero runtime and compiled modules created while loading extensions.
type ResourceSet interface {
	Close(ctx context.Context) error
}

type runtimeResources struct {
	runtime  wazero.Runtime
	compiled []wazero.CompiledModule

	closeOnce sync.Once
	closeErr  error
}

func newRuntimeResources(ctx context.Context) (*runtimeResources, error) {
	runtime := wazero.NewRuntime(ctx)
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, runtime); err != nil {
		_ = runtime.Close(ctx)
		return nil, err
	}
	return &runtimeResources{runtime: runtime}, nil
}

func (r *runtimeResources) track(compiled wazero.CompiledModule) {
	r.compiled = append(r.compiled, compiled)
}

func (r *runtimeResources) Close(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		var errs []error
		for i := len(r.compiled) - 1; i >= 0; i-- {
			errs = append(errs, r.compiled[i].Close(ctx))
		}
		if r.runtime != nil {
			errs = append(errs, r.runtime.Close(ctx))
		}
		r.closeErr = errors.Join(errs...)
	})
	return r.closeErr
}
