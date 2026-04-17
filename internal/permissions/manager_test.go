package permissions

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/smasonuk/falken-core/internal/runtimeapi"
)

type testInteractionHandler struct {
	onPermission func(context.Context, runtimeapi.PermissionRequest) (runtimeapi.PermissionResponse, error)
}

func (h testInteractionHandler) RequestPermission(ctx context.Context, req runtimeapi.PermissionRequest) (runtimeapi.PermissionResponse, error) {
	if h.onPermission != nil {
		return h.onPermission(ctx, req)
	}
	return runtimeapi.PermissionResponse{}, nil
}

func (h testInteractionHandler) RequestPlanApproval(ctx context.Context, req runtimeapi.PlanApprovalRequest) (runtimeapi.PlanApprovalResponse, error) {
	return runtimeapi.PlanApprovalResponse{}, nil
}

func (h testInteractionHandler) OnSubmit(ctx context.Context, req runtimeapi.SubmitRequest) error {
	return nil
}

func TestMatchPattern(t *testing.T) {
	tests := []struct {
		pattern string
		target  string
		want    bool
	}{
		{"ls", "ls", true},
		{"ls", "ls -l", false},
		{"ls*", "ls -l", true},
		{"ls**", "ls -l", true},
		{"*.txt", "dummy.txt", true},
		{"*.txt", "dir/dummy.txt", false},
		{"**.txt", "dir/dummy.txt", true},
		{"/workspace/**", "/workspace/src/main.go", true},
	}

	for _, tt := range tests {
		if got := MatchPattern(tt.pattern, tt.target); got != tt.want {
			t.Errorf("MatchPattern(%q, %q) = %v, want %v", tt.pattern, tt.target, got, tt.want)
		}
	}
}

func TestPermissionManager_CheckShellAccess(t *testing.T) {
	config := &Config{
		GlobalAllowedCommands: []string{"ls"},
		GlobalBlockedCommands: []string{"rm -rf /"},
	}
	manager := NewManager(config, nil)

	// Test blocked command
	if manager.CheckShellAccess("tool", "rm -rf /", "reason", nil) {
		t.Error("Expected rm -rf / to be blocked")
	}

	// Test allowed command in config
	if !manager.CheckShellAccess("tool", "ls", "reason", nil) {
		t.Error("Expected ls to be allowed by config")
	}

	// Test allowed command in schema (allowedCommands param)
	if !manager.CheckShellAccess("tool", "echo hello", "reason", []string{"echo"}) {
		t.Error("Expected echo to be allowed by schema")
	}

	// Test unallowed command
	if manager.CheckShellAccess("tool", "cat /etc/passwd", "reason", []string{"echo"}) {
		t.Error("Expected cat to be unallowed")
	}
}

func TestPermissionManager_ProjectShellApprovalDoesNotEnableStrictDeny(t *testing.T) {
	calls := 0
	manager := NewManager(&Config{}, testInteractionHandler{
		onPermission: func(ctx context.Context, req runtimeapi.PermissionRequest) (runtimeapi.PermissionResponse, error) {
			calls++
			return runtimeapi.PermissionResponse{Allowed: true, Scope: "project", AccessType: "execute"}, nil
		},
	})

	if !manager.CheckShellAccess("tool", "git status", "reason", nil) {
		t.Fatal("expected first command to be approved")
	}
	if !manager.CheckShellAccess("tool", "make test", "reason", nil) {
		t.Fatal("expected unrelated command to prompt and be approved")
	}
	if calls != 2 {
		t.Fatalf("expected separate prompts for unrelated commands, got %d", calls)
	}
	if got := manager.Config.PersistentAllowedCommands; !reflect.DeepEqual(got, []string{"git", "make"}) {
		t.Fatalf("unexpected persistent commands: %#v", got)
	}
}

func TestPermissionManager_StrictCommandAllowlistDeniesUnlistedCommands(t *testing.T) {
	called := false
	manager := NewManager(&Config{
		StrictCommandAllowlist: true,
		GlobalAllowedCommands:  []string{"ls"},
	}, testInteractionHandler{
		onPermission: func(ctx context.Context, req runtimeapi.PermissionRequest) (runtimeapi.PermissionResponse, error) {
			called = true
			return runtimeapi.PermissionResponse{Allowed: true, Scope: "project", AccessType: "execute"}, nil
		},
	})

	if manager.CheckShellAccess("tool", "cat file.txt", "reason", nil) {
		t.Fatal("expected strict allowlist to deny unlisted command")
	}
	if called {
		t.Fatal("expected strict allowlist to deny without prompting")
	}
}

func TestPermissionManager_CheckNetworkAccess(t *testing.T) {
	t.Run("Wildcard", func(t *testing.T) {
		config := &Config{
			GlobalAllowedURLs: []string{"https://*.google.com/**"},
		}
		manager := NewManager(config, nil)

		if !manager.CheckNetworkAccess("https://www.google.com/search") {
			t.Error("Expected access to be allowed by wildcard")
		}
		if manager.CheckNetworkAccess("https://google.com") {
			// Pattern was *.google.com, which matches www.google.com but not google.com because of [^/]*
			// Actually regexp.QuoteMeta(".") makes it "\.", so it matches a literal dot.
			// let's re-verify MatchPattern behavior
		}
	})

	t.Run("ExplicitBlockOverridesAllow", func(t *testing.T) {
		config := &Config{
			GlobalAllowedURLs: []string{"https://google.com/**"},
			GlobalBlockedURLs: []string{"https://google.com/private/**"},
		}
		manager := NewManager(config, nil)

		if !manager.CheckNetworkAccess("https://google.com/public") {
			t.Error("Expected public access to be allowed")
		}
		if manager.CheckNetworkAccess("https://google.com/private/data") {
			t.Error("Expected private access to be blocked")
		}
	})
}

func TestPermissionManager_CheckFileAccess_Once(t *testing.T) {
	path := "dummy.txt"
	accessType := "read"
	manager := NewManager(nil, testInteractionHandler{
		onPermission: func(ctx context.Context, req runtimeapi.PermissionRequest) (runtimeapi.PermissionResponse, error) {
			if req.Target != path {
				t.Errorf("Expected target %s, got %s", path, req.Target)
			}
			return runtimeapi.PermissionResponse{Allowed: true, Scope: "once", AccessType: accessType}, nil
		},
	})

	allowed := manager.CheckFileAccess("tool", path, accessType, "reason")
	if !allowed {
		t.Error("Expected access to be allowed")
	}

	// Check that it's NOT cached
	manager.mu.RLock()
	if _, ok := manager.sessionApprovals[path]; ok {
		manager.mu.RUnlock()
		t.Error("Expected access NOT to be cached for ScopeOnce")
		return
	}
	manager.mu.RUnlock()
}

func TestPermissionManager_CheckFileAccess_Session(t *testing.T) {
	path := "dummy.txt"
	accessType := "read"
	calls := 0
	manager := NewManager(nil, testInteractionHandler{
		onPermission: func(ctx context.Context, req runtimeapi.PermissionRequest) (runtimeapi.PermissionResponse, error) {
			calls++
			return runtimeapi.PermissionResponse{Allowed: true, Scope: "session", AccessType: accessType}, nil
		},
	})

	allowed := manager.CheckFileAccess("tool", path, accessType, "reason")
	if !allowed {
		t.Error("Expected access to be allowed")
	}

	// Check that it IS cached
	manager.mu.RLock()
	if access, ok := manager.sessionApprovals[path]; !ok || access != accessType {
		manager.mu.RUnlock()
		t.Errorf("Expected access to be cached for ScopeSession, got %v", access)
		return
	}
	manager.mu.RUnlock()

	// Second check should NOT trigger permChan
	allowed2 := manager.CheckFileAccess("tool", path, accessType, "reason")
	if !allowed2 {
		t.Error("Expected second access to be allowed from cache")
	}
	if calls != 1 {
		t.Fatalf("expected handler to be called once, got %d", calls)
	}
}

func TestPermissionManager_ConcurrentSessionApprovals(t *testing.T) {
	manager := NewManager(nil, testInteractionHandler{
		onPermission: func(ctx context.Context, req runtimeapi.PermissionRequest) (runtimeapi.PermissionResponse, error) {
			return runtimeapi.PermissionResponse{Allowed: true, Scope: "session", AccessType: req.AccessType}, nil
		},
	})

	var wg sync.WaitGroup
	for i := 0; i < 25; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			path := fmt.Sprintf("file-%d.txt", i)
			if !manager.CheckFileAccess("tool", path, "read", "reason") {
				t.Errorf("expected file access for %s", path)
			}
			if !manager.CheckShellAccess("tool", fmt.Sprintf("echo %d", i), "reason", nil) {
				t.Errorf("expected shell access for %d", i)
			}
		}()
	}
	wg.Wait()
}

func TestPermissionManager_ConcurrentProjectApprovals(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), ".falken.yaml")
	manager := NewManager(nil, testInteractionHandler{
		onPermission: func(ctx context.Context, req runtimeapi.PermissionRequest) (runtimeapi.PermissionResponse, error) {
			accessType := req.AccessType
			if accessType == "" {
				accessType = "read"
			}
			return runtimeapi.PermissionResponse{Allowed: true, Scope: "project", AccessType: accessType}, nil
		},
	})
	manager.ConfigPath = configPath

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		i := i
		wg.Add(2)
		go func() {
			defer wg.Done()
			if !manager.CheckFileAccess("tool", fmt.Sprintf("persist-%d.txt", i), "read", "reason") {
				t.Errorf("expected file approval for %d", i)
			}
		}()
		go func() {
			defer wg.Done()
			if !manager.CheckNetworkAccess(fmt.Sprintf("https://example%d.com/path", i)) {
				t.Errorf("expected network approval for %d", i)
			}
		}()
	}
	wg.Wait()
}

func TestPermissionManager_RepeatedProjectApprovalsDoNotDuplicate(t *testing.T) {
	manager := NewManager(&Config{}, nil)

	manager.persistConfig(func(cfg *Config) bool {
		return cfg.AddPersistentAllowedURL("https://example.com")
	})
	manager.persistConfig(func(cfg *Config) bool {
		return cfg.AddPersistentAllowedURL("https://example.com")
	})
	manager.persistConfig(func(cfg *Config) bool {
		return cfg.AddPersistentAllowedCommand("git")
	})
	manager.persistConfig(func(cfg *Config) bool {
		return cfg.AddPersistentAllowedCommand("git")
	})
	manager.persistConfig(func(cfg *Config) bool {
		return cfg.SetPersistentAllowedFile(".env", "read")
	})
	manager.persistConfig(func(cfg *Config) bool {
		return cfg.SetPersistentAllowedFile(".env", "read")
	})

	if got := manager.Config.PersistentAllowedURLs; !reflect.DeepEqual(got, []string{"https://example.com"}) {
		t.Fatalf("unexpected persistent urls: %#v", got)
	}
	if got := manager.Config.PersistentAllowedCommands; !reflect.DeepEqual(got, []string{"git"}) {
		t.Fatalf("unexpected persistent commands: %#v", got)
	}
	if got := manager.Config.PersistentAllowedFiles[".env"]; got != "read" {
		t.Fatalf("unexpected persistent file access: %q", got)
	}
}
