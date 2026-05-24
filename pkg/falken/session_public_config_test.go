package falken

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/smasonuk/falken-core/internal/policy"
)

func TestPublicConfigPolicyAndApprovalHandlerWireIntoRuntime(t *testing.T) {
	setStateHomeEnv(t)

	session, err := New(Config{
		WorkspaceDir:     publicAPITestWorkspace(t),
		ExecutionDetails: ExecutionConfig{Mode: ExecutionModeLocal},
		LLM:              noopLLM{},
		Policy: PolicyConfig{
			AllowedFiles: []FileRule{{
				Path:  "/workspace/",
				Match: MatchPrefix,
				Modes: []FileAccessMode{FileAccessRead},
			}},
		},
		ApprovalHandler: publicAPIApprovals{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	config := session.resources.runtime.policyEngine.Config()
	if len(config.AllowedFiles) != 1 || config.AllowedFiles[0].Path != "/workspace/" {
		t.Fatalf("public policy config was not wired into the runtime policy engine: %+v", config)
	}

	decision, err := session.resources.runtime.policyEngine.EvaluateShell(context.Background(), policy.ShellRequest{
		Command:          "echo hello",
		ApprovalRequired: true,
	})
	if err != nil {
		t.Fatalf("EvaluateShell: %v", err)
	}
	if !decision.Allowed || decision.Scope != policy.ApprovalScopeOnce {
		t.Fatalf("approval decision = %+v, want allowed once", decision)
	}
}

func TestPublicConfigMissingApprovalHandlerDeniesApprovalRequired(t *testing.T) {
	setStateHomeEnv(t)

	session, err := New(Config{
		WorkspaceDir:     publicAPITestWorkspace(t),
		ExecutionDetails: ExecutionConfig{Mode: ExecutionModeLocal},
		LLM:              noopLLM{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	decision, err := session.resources.runtime.policyEngine.EvaluateShell(context.Background(), policy.ShellRequest{
		Command:          "echo needs approval",
		ApprovalRequired: true,
	})
	if err != nil {
		t.Fatalf("EvaluateShell: %v", err)
	}
	if decision.Allowed || decision.Source != policy.DecisionSourceApprovalDenied {
		t.Fatalf("decision = %+v, want denied by default approval handler", decision)
	}
}

func TestProjectApprovalsPersistAcrossSessions(t *testing.T) {
	setStateHomeEnv(t)

	workspace := publicAPITestWorkspace(t)
	stateDir := filepath.Join(t.TempDir(), "state")
	filePath := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(filePath, []byte("notes"), 0o600); err != nil {
		t.Fatalf("write workspace file: %v", err)
	}

	first, err := New(Config{
		WorkspaceDir:     workspace,
		StateDir:         stateDir,
		ExecutionDetails: ExecutionConfig{Mode: ExecutionModeLocal},
		LLM:              noopLLM{},
		ApprovalHandler: projectApprovalHandler{
			fileScope: ApprovalScopeProject,
		},
	})
	if err != nil {
		t.Fatalf("New first: %v", err)
	}
	if err := first.Start(); err != nil {
		t.Fatalf("Start first: %v", err)
	}
	decision, err := first.resources.runtime.policyEngine.EvaluateFile(context.Background(), policy.FileRequest{
		Path:             filePath,
		Mode:             policy.FileAccessRead,
		ApprovalRequired: true,
	})
	if err != nil {
		t.Fatalf("EvaluateFile first: %v", err)
	}
	if !decision.Allowed || decision.Scope != policy.ApprovalScopeProject || decision.Source != policy.DecisionSourceApprovalGrantedProject {
		t.Fatalf("first decision = %+v, want project approval granted", decision)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close first: %v", err)
	}

	second, err := New(Config{
		WorkspaceDir:     workspace,
		StateDir:         stateDir,
		ExecutionDetails: ExecutionConfig{Mode: ExecutionModeLocal},
		LLM:              noopLLM{},
	})
	if err != nil {
		t.Fatalf("New second: %v", err)
	}
	if err := second.Start(); err != nil {
		t.Fatalf("Start second: %v", err)
	}
	decision, err = second.resources.runtime.policyEngine.EvaluateFile(context.Background(), policy.FileRequest{
		Path:             filePath,
		Mode:             policy.FileAccessRead,
		ApprovalRequired: true,
	})
	if err != nil {
		t.Fatalf("EvaluateFile second: %v", err)
	}
	if !decision.Allowed || decision.Source != policy.DecisionSourceProjectApproval {
		t.Fatalf("second decision = %+v, want persisted project approval", decision)
	}
}

type publicAPIApprovals struct{}

func (publicAPIApprovals) ApproveFile(context.Context, FileRequest) (ApprovalScope, error) {
	return ApprovalScopeDeny, nil
}

func (publicAPIApprovals) ApproveShell(context.Context, ShellRequest) (ApprovalScope, error) {
	return ApprovalScopeOnce, nil
}

func (publicAPIApprovals) ApproveNetwork(context.Context, NetworkRequest) (ApprovalScope, error) {
	return ApprovalScopeDeny, nil
}

func publicAPITestWorkspace(t *testing.T) string {
	t.Helper()
	return tempWorkspace(t)
}

type projectApprovalHandler struct {
	fileScope ApprovalScope
}

func (h projectApprovalHandler) ApproveFile(context.Context, FileRequest) (ApprovalScope, error) {
	return h.fileScope, nil
}

func (projectApprovalHandler) ApproveShell(context.Context, ShellRequest) (ApprovalScope, error) {
	return ApprovalScopeDeny, nil
}

func (projectApprovalHandler) ApproveNetwork(context.Context, NetworkRequest) (ApprovalScope, error) {
	return ApprovalScopeDeny, nil
}
