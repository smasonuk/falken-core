package host

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/smasonuk/falken-core/internal/network"
	"github.com/smasonuk/falken-core/internal/permissions"
	"github.com/smasonuk/falken-core/internal/runtimeapi"
)

func newTestShell(t *testing.T, pm *permissions.Manager) *StatefulShell {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return NewStatefulShell(cwd, t.TempDir(), pm, log.New(os.Stderr, "", 0))
}

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

func shouldSkipDockerTest(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "docker.sock") ||
		strings.Contains(msg, "operation not permitted") ||
		strings.Contains(msg, "No such image")
}

func TestStatefulShell_SandboxLifecycle(t *testing.T) {
	if os.Getenv("SKIP_DOCKER_TESTS") == "true" {
		t.Skip("Skipping Docker tests")
	}

	pm := permissions.NewManager(&permissions.Config{
		GlobalAllowedURLs: []string{"google.com"},
	}, nil)

	// Initialize proxy to generate cert
	shell := newTestShell(t, pm)
	proxy, err := network.NewSandboxProxy("8081", pm, filepath.Join(shell.StateDir, "cache"))
	if err != nil {
		t.Fatalf("failed to create proxy: %v", err)
	}
	if err := proxy.Start(); err != nil {
		if strings.Contains(err.Error(), "operation not permitted") {
			t.Skipf("Skipping Docker test in restricted environment: %v", err)
		}
		t.Fatalf("failed to start proxy: %v", err)
	}
	shell.ProxyPort = proxy.Port

	shell.TestingMode = true // Allow local execution for test

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	imageName := "falken/sandbox:latest"
	err = shell.StartSandbox(ctx, imageName)
	if err != nil {
		if shouldSkipDockerTest(err) {
			t.Skipf("Skipping Docker test in restricted environment: %v", err)
		}
		t.Fatalf("StartSandbox failed: %v", err)
	}

	if shell.containerID == "" {
		t.Fatal("expected containerID to be set")
	}

	err = shell.Close(ctx)
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

func TestStatefulShell_SandboxExecute(t *testing.T) {
	if os.Getenv("SKIP_DOCKER_TESTS") == "true" {
		t.Skip("Skipping Docker tests")
	}

	pm := permissions.NewManager(&permissions.Config{
		GlobalAllowedURLs: []string{"google.com"},
	}, nil)

	// Initialize proxy to generate cert
	shell := newTestShell(t, pm)
	proxy, err := network.NewSandboxProxy("8080", pm, filepath.Join(shell.StateDir, "cache"))
	if err != nil {
		t.Fatalf("failed to create proxy: %v", err)
	}
	if err := proxy.Start(); err != nil {
		if strings.Contains(err.Error(), "operation not permitted") {
			t.Skipf("Skipping Docker test in restricted environment: %v", err)
		}
		t.Fatalf("failed to start proxy: %v", err)
	}
	shell.ProxyPort = proxy.Port

	shell.TestingMode = true // Allow local execution for test

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	imageName := "falken/sandbox:latest"
	err = shell.StartSandbox(ctx, imageName)
	if err != nil {
		if shouldSkipDockerTest(err) {
			t.Skipf("Skipping Docker test in restricted environment: %v", err)
		}
		t.Fatalf("StartSandbox failed: %v", err)
	}
	defer shell.Close(ctx)

	// Test a simple command
	out, err := shell.Execute(ctx, "test", "echo 'hello sandbox'", "test", []string{"echo"}, false, nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if strings.TrimSpace(out) != "hello sandbox" {
		t.Errorf("expected 'hello sandbox', got %q", out)
	}

	// Test working directory mapping
	// Create a subdir in the sandbox directory (since it's ephemeral)
	tempDirName := "test_sandbox_subdir"
	tempDirPath := filepath.Join(shell.SandboxCWD, tempDirName)
	if err := os.MkdirAll(tempDirPath, 0755); err != nil {
		t.Fatalf("failed to create sandbox subdir: %v", err)
	}

	_, err = shell.Execute(ctx, "test", "cd ./"+tempDirName, "test", []string{"cd"}, false, nil)
	if err != nil {
		t.Fatalf("cd failed: %v", err)
	}

	out, err = shell.Execute(ctx, "test", "pwd", "test", []string{"pwd"}, false, nil)
	if err != nil {
		t.Fatalf("pwd failed: %v", err)
	}

	if !strings.Contains(strings.TrimSpace(out), tempDirName) {
		t.Errorf("expected pwd to contain %s, got %q", tempDirName, strings.TrimSpace(out))
	}
}

func TestStatefulShell_CD(t *testing.T) {
	pm := permissions.NewManager(nil, nil)
	shell := newTestShell(t, pm)
	shell.TestingMode = true // Allow local execution for test

	// Use a directory within the current workspace to avoid "escapes workspace root" error
	tempDir, err := os.MkdirTemp(".", "test_cd")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	absTempDir, err := filepath.Abs(tempDir)
	if err != nil {
		t.Fatalf("failed to get absolute path: %v", err)
	}

	_, err = shell.Execute(context.Background(), "test", "cd "+tempDir, "test", []string{"cd"}, false, nil)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	// resolvePath returns the absolute path
	if shell.RealCWD != absTempDir {
		t.Errorf("expected RealCWD to be %s, got %s", absTempDir, shell.RealCWD)
	}
}

func TestStatefulShell_Env(t *testing.T) {
	config := &permissions.Config{
		GlobalAllowedCommands: []string{"export**", "echo**", "sh**"},
	}
	pm := permissions.NewManager(config, nil)
	shell := newTestShell(t, pm)
	shell.TestingMode = true // Allow local execution for test

	_, err := shell.Execute(context.Background(), "test", "export FOO=bar", "test", []string{"export"}, false, nil)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	found := false
	for _, env := range shell.EnvVars {
		if env == "FOO=bar" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected FOO=bar in EnvVars, got %v", shell.EnvVars)
	}

	out, err := shell.Execute(context.Background(), "test", "echo $FOO", "test", []string{"sh"}, false, nil)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	if strings.TrimSpace(out) != "bar" {
		t.Errorf("expected output to be 'bar', got '%s'", strings.TrimSpace(out))
	}
}

func TestStatefulShell_ExecFail(t *testing.T) {
	config := &permissions.Config{
		GlobalAllowedCommands: []string{"ls**"},
	}
	pm := permissions.NewManager(config, nil)

	shell := newTestShell(t, pm)
	shell.TestingMode = true // Allow local execution for test

	out, err := shell.Execute(context.Background(), "test", "ls /nonexistent_directory_12345", "test", []string{"ls"}, false, nil)
	if err == nil {
		t.Errorf("expected error, got none")
	}

	if !strings.Contains(out, "No such file or directory") && !strings.Contains(out, "No such file") {
		t.Errorf("expected output to contain error message, got: %s", out)
	}
}

func TestStatefulShell_FormatOutputPersistsTruncation(t *testing.T) {
	shell := newTestShell(t, permissions.NewManager(nil, nil))

	input := strings.Repeat("line\n", 3000)
	output := shell.formatOutput(input)

	if !strings.Contains(output, "[TRUNCATED:") {
		t.Fatalf("expected truncated marker, got %q", output)
	}

	paths := runtimeapi.Paths{StateDir: shell.StateDir}
	truncDir := paths.TruncationDir()
	entries, err := os.ReadDir(truncDir)
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("expected truncation file to be written")
	}
}

func TestStatefulShell_ExecuteEnforcesAllowedCommands(t *testing.T) {
	config := &permissions.Config{
		GlobalBlockedCommands: []string{"rm -rf /"},
	}
	pm := permissions.NewManager(config, nil)
	shell := newTestShell(t, pm)
	shell.TestingMode = true

	out, err := shell.Execute(context.Background(), "test", "echo hello", "test", []string{"echo"}, false, nil)
	if err != nil {
		t.Fatalf("expected allowed command to execute, got %v", err)
	}
	if strings.TrimSpace(out) != "hello" {
		t.Fatalf("expected output %q, got %q", "hello", out)
	}
}

func TestStatefulShell_ExecuteBlocksDeniedCommands(t *testing.T) {
	config := &permissions.Config{
		GlobalBlockedCommands: []string{"cat /etc/passwd"},
	}
	pm := permissions.NewManager(config, nil)
	shell := newTestShell(t, pm)
	shell.TestingMode = true

	out, err := shell.Execute(context.Background(), "test", "cat /etc/passwd", "test", nil, false, nil)
	if err == nil {
		t.Fatal("expected denied command to fail before execution")
	}
	if out != "" {
		t.Fatalf("expected no output for denied command, got %q", out)
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("expected permission denied error, got %v", err)
	}
}

func TestStatefulShell_ExecuteUsesSessionApproval(t *testing.T) {
	var calls atomic.Int32
	pm := permissions.NewManager(nil, testInteractionHandler{
		onPermission: func(ctx context.Context, req runtimeapi.PermissionRequest) (runtimeapi.PermissionResponse, error) {
			calls.Add(1)
			return runtimeapi.PermissionResponse{
				Allowed:    true,
				Scope:      "session",
				AccessType: "execute",
			}, nil
		},
	})
	shell := newTestShell(t, pm)
	shell.TestingMode = true

	out, err := shell.Execute(context.Background(), "test", "printf first", "test", nil, false, nil)
	if err != nil {
		t.Fatalf("first execution failed: %v", err)
	}
	if out != "first" {
		t.Fatalf("expected first output %q, got %q", "first", out)
	}

	out, err = shell.Execute(context.Background(), "test", "printf second", "test", nil, false, nil)
	if err != nil {
		t.Fatalf("second execution failed: %v", err)
	}
	if out != "second" {
		t.Fatalf("expected second output %q, got %q", "second", out)
	}

	if calls.Load() != 1 {
		t.Fatalf("expected one permission prompt, got %d", calls.Load())
	}
}

func TestStatefulShell_CDHonorsPermissions(t *testing.T) {
	config := &permissions.Config{
		GlobalAllowedCommands: []string{"cd**"},
	}
	pm := permissions.NewManager(config, nil)
	shell := newTestShell(t, pm)
	shell.TestingMode = true

	tempDir, err := os.MkdirTemp(".", "test_cd_perms")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	absTempDir, err := filepath.Abs(tempDir)
	if err != nil {
		t.Fatalf("failed to get absolute path: %v", err)
	}

	_, err = shell.Execute(context.Background(), "test", "cd "+tempDir, "test", nil, false, nil)
	if err != nil {
		t.Fatalf("expected cd to succeed, got %v", err)
	}
	if shell.RealCWD != absTempDir {
		t.Fatalf("expected RealCWD %s, got %s", absTempDir, shell.RealCWD)
	}
}

func TestStatefulShell_ExportHonorsPermissions(t *testing.T) {
	config := &permissions.Config{
		GlobalAllowedCommands: []string{"export**"},
	}
	pm := permissions.NewManager(config, nil)
	shell := newTestShell(t, pm)
	shell.TestingMode = true

	_, err := shell.Execute(context.Background(), "test", "export FOO=bar", "test", nil, false, nil)
	if err != nil {
		t.Fatalf("expected export to succeed, got %v", err)
	}

	found := false
	for _, env := range shell.EnvVars {
		if env == "FOO=bar" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected FOO=bar in EnvVars, got %v", shell.EnvVars)
	}
}

func TestStatefulShell_SandboxBackgroundProcessLifecycle(t *testing.T) {
	if os.Getenv("SKIP_DOCKER_TESTS") == "true" {
		t.Skip("Skipping Docker tests")
	}

	pm := permissions.NewManager(&permissions.Config{
		GlobalAllowedURLs: []string{"google.com"},
	}, nil)

	shell := newTestShell(t, pm)
	proxy, err := network.NewSandboxProxy("8082", pm, filepath.Join(shell.StateDir, "cache"))
	if err != nil {
		t.Fatalf("failed to create proxy: %v", err)
	}
	if err := proxy.Start(); err != nil {
		if strings.Contains(err.Error(), "operation not permitted") {
			t.Skipf("Skipping Docker test in restricted environment: %v", err)
		}
		t.Fatalf("failed to start proxy: %v", err)
	}
	shell.ProxyPort = proxy.Port
	shell.TestingMode = true

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := shell.StartSandbox(ctx, "falken/sandbox:latest"); err != nil {
		if shouldSkipDockerTest(err) {
			t.Skipf("Skipping Docker test in restricted environment: %v", err)
		}
		t.Fatalf("StartSandbox failed: %v", err)
	}
	defer shell.Close(ctx)

	result, err := shell.startProcess("while true; do echo tick; sleep 1; done")
	if err != nil {
		t.Fatalf("startProcess failed: %v", err)
	}
	if !strings.Contains(result, "Started process") {
		t.Fatalf("unexpected start result: %q", result)
	}

	var process *BackgroundProcess
	var processID string
	for id, candidate := range shell.Backgrounds {
		processID = id
		process = candidate
		break
	}
	if process == nil || process.PID == 0 {
		t.Fatalf("expected sandbox process metadata, got %#v", process)
	}

	time.Sleep(1500 * time.Millisecond)

	logs, err := shell.readProcessLogs(processID)
	if err != nil {
		t.Fatalf("readProcessLogs failed: %v", err)
	}
	if !strings.Contains(logs, "tick") {
		t.Fatalf("expected logs to contain tick, got %q", logs)
	}

	checkCmd := fmt.Sprintf("ps -p %d >/dev/null && echo running", process.PID)
	out, err := shell.containerExecOutput(ctx, checkCmd)
	if err != nil {
		t.Fatalf("expected sandbox process to be visible in container, got %v", err)
	}
	if strings.TrimSpace(out) != "running" {
		t.Fatalf("expected running marker, got %q", out)
	}

	if _, err := shell.killProcess(processID); err != nil {
		t.Fatalf("killProcess failed: %v", err)
	}

	if _, err := shell.containerExecOutput(ctx, fmt.Sprintf("kill -0 %d", process.PID)); err == nil {
		t.Fatalf("expected sandbox process %d to be stopped", process.PID)
	}
}

func TestStatefulShell_CloseCleansBackgroundProcesses(t *testing.T) {
	if os.Getenv("SKIP_DOCKER_TESTS") == "true" {
		t.Skip("Skipping Docker tests")
	}

	pm := permissions.NewManager(&permissions.Config{
		GlobalAllowedURLs: []string{"google.com"},
	}, nil)

	shell := newTestShell(t, pm)
	proxy, err := network.NewSandboxProxy("8083", pm, filepath.Join(shell.StateDir, "cache"))
	if err != nil {
		t.Fatalf("failed to create proxy: %v", err)
	}
	if err := proxy.Start(); err != nil {
		if strings.Contains(err.Error(), "operation not permitted") {
			t.Skipf("Skipping Docker test in restricted environment: %v", err)
		}
		t.Fatalf("failed to start proxy: %v", err)
	}
	shell.ProxyPort = proxy.Port
	shell.TestingMode = true

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := shell.StartSandbox(ctx, "falken/sandbox:latest"); err != nil {
		if shouldSkipDockerTest(err) {
			t.Skipf("Skipping Docker test in restricted environment: %v", err)
		}
		t.Fatalf("StartSandbox failed: %v", err)
	}

	if _, err := shell.startProcess("sleep 30"); err != nil {
		t.Fatalf("startProcess failed: %v", err)
	}
	if len(shell.Backgrounds) != 1 {
		t.Fatalf("expected one background process, got %d", len(shell.Backgrounds))
	}

	if err := shell.Close(ctx); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if len(shell.Backgrounds) != 0 {
		t.Fatalf("expected background map to be cleaned up, got %d entries", len(shell.Backgrounds))
	}
}
