package runtimeapi

import "context"

// PermissionRequest asks the host to approve access to a protected resource.
type PermissionRequest struct {
	Kind       string
	Target     string
	AccessType string
}

// PermissionResponse captures the host's decision for a PermissionRequest.
type PermissionResponse struct {
	Allowed    bool
	Scope      string
	AccessType string
}

// PlanApprovalRequest asks the host to accept or reject a proposed plan.
type PlanApprovalRequest struct {
	Plan string
}

// PlanApprovalResponse records the host's answer to a plan approval prompt.
type PlanApprovalResponse struct {
	Approved bool
	Feedback string
}

// SubmitRequest reports that the runtime is handing completed work back to the host.
type SubmitRequest struct {
	Summary string
}

// InteractionHandler bridges runtime prompts that require user or host decisions.
type InteractionHandler interface {
	RequestPermission(ctx context.Context, req PermissionRequest) (PermissionResponse, error)
	RequestPlanApproval(ctx context.Context, req PlanApprovalRequest) (PlanApprovalResponse, error)
	OnSubmit(ctx context.Context, req SubmitRequest) error
}

// NopInteractionHandler is a permissive no-op handler for non-interactive embeddings.
type NopInteractionHandler struct{}

func (NopInteractionHandler) RequestPermission(ctx context.Context, req PermissionRequest) (PermissionResponse, error) {
	return PermissionResponse{}, nil
}

func (NopInteractionHandler) RequestPlanApproval(ctx context.Context, req PlanApprovalRequest) (PlanApprovalResponse, error) {
	return PlanApprovalResponse{}, nil
}

func (NopInteractionHandler) OnSubmit(ctx context.Context, req SubmitRequest) error {
	return nil
}
