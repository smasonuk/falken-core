// Package llm contains adapter helpers for integrating real chat providers with
// Falken's provider-neutral LLM interface.
package llm

import (
	"context"
	"errors"

	"github.com/smasonuk/falken-core/pkg/falken"
)

// ErrProviderRequired indicates an adapter was used without a provider.
var ErrProviderRequired = errors.New("llm provider is required")

// Provider is the minimal provider-side contract that can be wrapped as a
// falken.LLM. Providers use pkg/falken completion types directly; older
// adapter-layer mirror types and field-by-field converters have been removed.
type Provider interface {
	Complete(context.Context, falken.CompletionRequest) (falken.CompletionResponse, error)
}

// StreamingProvider is an optional provider-side contract for incremental assistant text.
type StreamingProvider interface {
	Provider
	StreamComplete(context.Context, falken.CompletionRequest, falken.AssistantTextSink) (falken.CompletionResponse, error)
}

// Adapter wraps a Provider and satisfies falken.LLM.
type Adapter struct {
	Provider Provider
}

// New returns a Falken LLM backed by provider.
func New(provider Provider) *Adapter {
	return &Adapter{Provider: provider}
}

// Complete forwards Falken request/response types to the wrapped provider.
func (a *Adapter) Complete(ctx context.Context, request falken.CompletionRequest) (falken.CompletionResponse, error) {
	if a == nil || a.Provider == nil {
		return falken.CompletionResponse{}, ErrProviderRequired
	}
	return a.Provider.Complete(ctx, request)
}

// StreamComplete preserves streaming when the wrapped Provider implements StreamingProvider.
func (a *Adapter) StreamComplete(ctx context.Context, request falken.CompletionRequest, sink falken.AssistantTextSink) (falken.CompletionResponse, error) {
	if a == nil || a.Provider == nil {
		return falken.CompletionResponse{}, ErrProviderRequired
	}
	streaming, ok := a.Provider.(StreamingProvider)
	if !ok {
		return a.Complete(ctx, request)
	}
	return streaming.StreamComplete(ctx, request, sink)
}
