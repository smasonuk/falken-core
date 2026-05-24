package runtimefiles

import (
	"errors"

	"github.com/smasonuk/falken-core/internal/files"
)

// ErrServiceRequired indicates the runtime file facade was created without a managed file service.
var ErrServiceRequired = errors.New("managed file service is required")

// Operations is the runtime-facing facade over the managed file service.
type Operations struct {
	service *files.Service
}

// NewOperations creates a runtime-facing file operation facade.
func NewOperations(service *files.Service) (*Operations, error) {
	if service == nil {
		return nil, ErrServiceRequired
	}

	return &Operations{service: service}, nil
}

// ManagedService returns the underlying managed file service for lower-level runtime integrations.
func (o *Operations) ManagedService() *files.Service {
	return o.service
}
