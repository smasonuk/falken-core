package host

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/smasonuk/falken-core/internal/bash"
	"github.com/smasonuk/falken-core/internal/permissions"
	"github.com/smasonuk/falken-core/internal/runtimeapi"
	"github.com/smasonuk/falken-core/internal/runtimectx"

	"mvdan.cc/sh/v3/syntax"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

type StatefulShell struct {
	RealCWD      string
	SandboxCWD   string
	WorkspaceDir string
	StateDir     string
	ProxyPort    string
	EnvVars      []string
	ContainerEnv []string
	PermManager  *permissions.Manager
	Logger       *log.Logger
	HasGit       bool
	TestingMode  bool

	mu          sync.Mutex
	Backgrounds map[string]*BackgroundProcess

	dockerClient *client.Client
	containerID  string
	mappedUser   string

	sandboxManager *sandboxManager
	processManager *processManager
	pathResolver   *pathResolver
	outputStore    *outputStore
}

type BackgroundProcess struct {
	Command  string
	PID      int
	LogPath  string
	LocalCmd *exec.Cmd
	LogBuf   *bytes.Buffer
}

func NewStatefulShell(workspaceDir, stateDir string, permManager *permissions.Manager, logger *log.Logger) *StatefulShell {
	if logger == nil {
		logger = log.New(os.Stderr, "", 0)
	}

	return &StatefulShell{
		RealCWD:        workspaceDir,
		WorkspaceDir:   workspaceDir,
		StateDir:       stateDir,
		ProxyPort:      "8080",
		EnvVars:        []string{},
		PermManager:    permManager,
		Logger:         logger,
		Backgrounds:    make(map[string]*BackgroundProcess),
		sandboxManager: &sandboxManager{},
		processManager: &processManager{},
		pathResolver:   &pathResolver{},
		outputStore:    &outputStore{},
	}
}

func (s *StatefulShell) StartSandbox(ctx context.Context, imageName string) error {
	return s.sandboxManager.start(s, ctx, imageName)
}

func (s *StatefulShell) Close(ctx context.Context) error {
	return s.sandboxManager.close(s, ctx)
}

// ExportHostFunctions exports the host functions to the Wazero runtime environment.
func (s *StatefulShell) ExportHostFunctions(ctx context.Context, r wazero.Runtime) error {
	_, err := r.NewHostModuleBuilder("env").
		NewFunctionBuilder().
		WithFunc(s.hostExecShell).
		Export("host_exec_shell").
		NewFunctionBuilder().
		WithFunc(s.hostFetchURL).
		Export("host_fetch_url").
		NewFunctionBuilder().
		WithFunc(s.hostStartProcess).
		Export("host_start_process").
		NewFunctionBuilder().
		WithFunc(s.hostReadProcessLogs).
		Export("host_read_process_logs").
		NewFunctionBuilder().
		WithFunc(s.hostKillProcess).
		Export("host_kill_process").
		NewFunctionBuilder().
		WithFunc(s.hostGetState).
		Export("host_get_state").
		NewFunctionBuilder().
		WithFunc(s.hostSetState).
		Export("host_set_state").
		NewFunctionBuilder().
		WithFunc(s.hostBackupFile).
		Export("host_backup_file").
		Instantiate(ctx)
	return err
}

func (s *StatefulShell) hostBackupFile(ctx context.Context, m api.Module, pathPtr, pathLen, contentPtr, contentLen uint32) uint32 {
	pathBytes, ok := m.Memory().Read(pathPtr, pathLen)
	if !ok {
		return s.writeError(ctx, m, fmt.Errorf("failed to read path from memory"))
	}
	originalPath := string(pathBytes)

	contentBytes, ok := m.Memory().Read(contentPtr, contentLen)
	if !ok {
		return s.writeError(ctx, m, fmt.Errorf("failed to read content from memory"))
	}

	backupDir := filepath.Join(s.StateDir, "backups")
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return s.writeError(ctx, m, err)
	}

	// First-Touch Rule: Use absolute path hash or simple flat name, no timestamp
	safeName := strings.ReplaceAll(originalPath, string(filepath.Separator), "_")
	backupPath := filepath.Join(backupDir, fmt.Sprintf("%s.bak", safeName))

	// Check if it already exists (First-Touch)
	if _, err := os.Stat(backupPath); err == nil {
		// Already backed up this session, skip.
		return s.writeStringToMemory(ctx, m, "success")
	}

	if err := os.WriteFile(backupPath, contentBytes, 0644); err != nil {
		return s.writeError(ctx, m, err)
	}

	return s.writeStringToMemory(ctx, m, "success")
}

// hostExecShell is the function exported to Wasm.
// It takes a pointer and length to a command string in Wasm memory.
// It returns a pointer to the result string in Wasm memory.
type streamWriter struct {
	cb func(string)
}

func (w streamWriter) Write(p []byte) (n int, err error) {
	if w.cb != nil {
		w.cb(string(p))
	}
	return len(p), nil
}

func (s *StatefulShell) hostExecShell(ctx context.Context, m api.Module, cmdPtr, cmdLen uint32) uint32 {
	cmdBytes, ok := m.Memory().Read(cmdPtr, cmdLen)
	if !ok {
		return s.writeStringToMemory(ctx, m, "error: failed to read command from memory")
	}

	var command string
	if err := json.Unmarshal(cmdBytes, &command); err != nil {
		return s.writeStringToMemory(ctx, m, "error: command must be a JSON-encoded string")
	}

	// Extract granular permissions
	perms, ok := runtimectx.Permissions(ctx)
	if !ok {
		return s.writeError(ctx, m, fmt.Errorf("security violation: missing granular permissions"))
	}

	// Extract sandbox_only
	sandboxOnly, _ := runtimectx.SandboxOnly(ctx)

	// Verify command prefix against Permissions.Shell
	granted := false
	for _, prefix := range perms.Shell {
		if strings.HasPrefix(command, prefix) {
			granted = true
			break
		}
	}

	// ONLY strictly enforce the YAML limits for Plugins (AOT).
	// Tools (JIT) are allowed to ask for things outside their requested_permissions,
	// and the PermManager/TUI will prompt the user to allow/deny them.
	if sandboxOnly && !granted {
		return s.writeError(ctx, m, fmt.Errorf("security violation: plugin strictly forbidden from running %q", command))
	}

	toolName := "unknown_plugin"
	if name, ok := runtimectx.ToolName(ctx); ok && name != "" {
		toolName = name
	}

	result, err := s.Execute(ctx, toolName, command, "requested by wasm", perms.Shell, sandboxOnly, nil)
	if err != nil {
		result = fmt.Sprintf("error: %v\n%s", err, result)
	}

	return s.writeStringToMemory(ctx, m, result)
}

func (s *StatefulShell) Execute(ctx context.Context, toolName string, command string, reason string, allowedCommands []string, sandboxOnly bool, onChunk func(string)) (string, error) {
	if command == "" {
		return "", fmt.Errorf("no command provided")
	}
	s.Logger.Printf("HOST OP [shell]: Tool=%q Cmd=%q SandboxOnly=%v", toolName, command, sandboxOnly)

	if s.PermManager != nil && !s.PermManager.CheckShellAccess(toolName, command, reason, allowedCommands) {
		err := fmt.Errorf("permission denied: command %q blocked by shell policy", command)
		s.Logger.Printf("HOST ERR [shell]: %v", err)
		return "", err
	}

	// Special handling for top-level cd and export to maintain state
	// We still need this to track CWD and EnvVars on the host side.
	f, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err == nil && len(f.Stmts) == 1 {
		stmt := f.Stmts[0]
		if call, ok := stmt.Cmd.(*syntax.CallExpr); ok {
			cmdName := ""
			if len(call.Args) > 0 {
				arg := call.Args[0]
				for _, part := range arg.Parts {
					if lit, ok := part.(*syntax.Lit); ok {
						cmdName += lit.Value
					}
				}
			}

			if cmdName == "cd" {
				if len(call.Args) < 2 {
					return "", fmt.Errorf("cd: missing argument")
				}
				targetDir := ""
				if len(call.Args[1].Parts) == 1 {
					if lit, ok := call.Args[1].Parts[0].(*syntax.Lit); ok {
						targetDir = lit.Value
					}
				}

				newCWD, err := s.resolvePath(targetDir)
				if err != nil {
					return "", err
				}

				// Check if it exists either on host or in sandbox
				info, err := os.Stat(newCWD)
				if err != nil {
					// If not on host, it might be in the sandbox
					if s.SandboxCWD != "" {
						sandboxPath := newCWD
						if !strings.HasPrefix(newCWD, s.SandboxCWD) {
							// If newCWD is a real host path, map it to the sandbox path
							rel, relErr := filepath.Rel(s.RealCWD, newCWD)
							if relErr == nil {
								sandboxPath = filepath.Join(s.SandboxCWD, rel)
							}
						}
						info, err = os.Stat(sandboxPath)
					}
				}

				if err != nil || !info.IsDir() {
					return "", fmt.Errorf("cd: no such file or directory: %s", targetDir)
				}
				s.RealCWD = newCWD
				s.Logger.Printf("HOST SUCCESS [cd]: %s", s.RealCWD)
				return "", nil
			}

			if cmdName == "export" {
				if len(call.Args) < 2 {
					return "", fmt.Errorf("export: missing argument")
				}
				for _, arg := range call.Args[1:] {
					// Handle key=val assignments
					argVal := ""
					for _, part := range arg.Parts {
						if lit, ok := part.(*syntax.Lit); ok {
							argVal += lit.Value
						}
					}
					if argVal != "" {
						parts := strings.SplitN(argVal, "=", 2)
						if len(parts) == 2 {
							key := parts[0]
							val := strings.Trim(parts[1], `"'`)
							s.setEnv(key, val)
							s.Logger.Printf("HOST SUCCESS [export]: %s=%s", key, val)
						}
					}
				}
				return "", nil
			}
		}

		if decl, ok := stmt.Cmd.(*syntax.DeclClause); ok {
			cmdName := ""
			if decl.Variant != nil {
				cmdName = decl.Variant.Value
			}
			if cmdName == "export" {
				for _, assign := range decl.Args {
					if assign.Name != nil && assign.Value != nil {
						key := assign.Name.Value
						val := ""
						for _, part := range assign.Value.Parts {
							if lit, ok := part.(*syntax.Lit); ok {
								val += lit.Value
							}
						}
						val = strings.Trim(val, `"'`)
						s.setEnv(key, val)
						s.Logger.Printf("HOST SUCCESS [export]: %s=%s", key, val)
					}
				}
				return "", nil
			}
		}
	}

	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()

	// Wrapper for streaming
	streamOut := streamWriter{cb: onChunk}

	// If sandboxed, use docker exec
	if s.dockerClient != nil && s.containerID != "" {
		execConfig := container.ExecOptions{
			Cmd:          []string{"bash", "-c", command},
			WorkingDir:   s.containerWorkingDir(),
			Env:          s.containerExecEnv(),
			User:         s.mappedUser,
			AttachStdout: true,
			AttachStderr: true,
		}

		execID, err := s.dockerClient.ContainerExecCreate(ctx, s.containerID, execConfig)
		if err != nil {
			return "", err
		}

		resp, err := s.dockerClient.ContainerExecAttach(ctx, execID.ID, container.ExecStartOptions{
			Tty: false,
		})
		if err != nil {
			return "", err
		}
		defer resp.Close()
		done := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				resp.Close()
			case <-done:
			}
		}()
		defer close(done)

		var outBuf bytes.Buffer
		var errBuf bytes.Buffer

		outWriter := io.MultiWriter(&outBuf, streamOut)
		errWriter := io.MultiWriter(&errBuf, streamOut)

		_, err = stdcopy.StdCopy(outWriter, errWriter, resp.Reader)
		if err != nil {
			if ctx.Err() != nil {
				return outBuf.String(), ctx.Err()
			}
			return "", err
		}

		output := outBuf.String()
		if errBuf.Len() > 0 {
			if output != "" && !strings.HasSuffix(output, "\n") {
				output += "\n"
			}
			output += errBuf.String()
		}

		inspect, err := s.dockerClient.ContainerExecInspect(ctx, execID.ID)
		if err != nil {
			if ctx.Err() != nil {
				return output, ctx.Err()
			}
			return output, err
		}

		isError, semanticMsg := bash.InterpretExitCode(command, inspect.ExitCode)
		if !isError && semanticMsg != "" {
			if output != "" && !strings.HasSuffix(output, "\n") {
				output += "\n"
			}
			output += semanticMsg
			err = nil
		} else if inspect.ExitCode != 0 {
			err = fmt.Errorf("exit code %d", inspect.ExitCode)
		}

		output = s.formatOutput(output)
		if err != nil {
			s.Logger.Printf("HOST ERR [shell]: %v Output=%q", err, output)
		} else {
			s.Logger.Printf("HOST SUCCESS [shell]: %d bytes", len(output))
		}
		return output, err
	}

	// We never fall back to local host execution in production to prevent sandbox escapes.
	if !s.TestingMode {
		return "", fmt.Errorf("FATAL: Sandbox is not active. Local host execution is strictly disabled for security. Please restart the sandbox.")
	}

	// Fallback to host execution ONLY if TestingMode is true (for unit tests)
	if sandboxOnly {
		return "", fmt.Errorf("security violation: plugin requires sandbox execution but no sandbox is active")
	}

	// We'll use "bash" if available, otherwise "sh"
	shellBin := "bash"
	if _, err := exec.LookPath("bash"); err != nil {
		shellBin = "sh"
	}

	cmd := exec.CommandContext(ctx, shellBin, "-c", command)
	cmd.Dir = s.RealCWD
	cmd.Env = s.EnvVars

	// Capture interleaved stdout and stderr
	var combinedBuf bytes.Buffer
	combinedWriter := io.MultiWriter(&combinedBuf, streamOut)
	cmd.Stdout = combinedWriter
	cmd.Stderr = combinedWriter

	err = cmd.Run()
	output := combinedBuf.String()
	if ctx.Err() != nil {
		err = ctx.Err()
	}

	// Semantic Exit Codes
	exitCode := 0
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode = exitError.ExitCode()
		} else {
			return output, err
		}
	}

	isError, semanticMsg := bash.InterpretExitCode(command, exitCode)
	if !isError && semanticMsg != "" {
		if output != "" && !strings.HasSuffix(output, "\n") {
			output += "\n"
		}
		output += semanticMsg
		err = nil
	}

	// Smart Output Truncation
	output = s.formatOutput(output)

	if err != nil {
		s.Logger.Printf("HOST ERR [shell]: %v Output=%q", err, output)
	} else {
		s.Logger.Printf("HOST SUCCESS [shell]: %d bytes", len(output))
	}

	return output, err
}

func (s *StatefulShell) setEnv(key, val string) {
	found := false
	for i, env := range s.EnvVars {
		if strings.HasPrefix(env, key+"=") {
			s.EnvVars[i] = key + "=" + val
			found = true
			break
		}
	}
	if !found {
		s.EnvVars = append(s.EnvVars, key+"="+val)
	}
}

func (s *StatefulShell) formatOutput(output string) string {
	return s.outputStore.formatOutput(s, output)
}

func (s *StatefulShell) hostFetchURL(ctx context.Context, m api.Module, urlPtr, urlLen uint32) uint32 {
	urlBytes, _ := m.Memory().Read(urlPtr, urlLen)
	url := string(urlBytes)

	// Simple domain extraction: everything before the first / after ://
	domain := url
	if idx := strings.Index(url, "://"); idx != -1 {
		domain = url[idx+3:]
	}
	if idx := strings.Index(domain, "/"); idx != -1 {
		domain = domain[:idx]
	}

	// Extract granular permissions
	perms, ok := runtimectx.Permissions(ctx)
	if !ok {
		return s.writeError(ctx, m, fmt.Errorf("security violation: missing granular permissions"))
	}

	// Verify against granular permissions
	granted := false
	for _, rule := range perms.Network {
		if rule.Domain == "*" { // Support catch-all
			granted = true
			break
		}
		if rule.URL != "" && rule.URL == url {
			granted = true
			break
		}
		if rule.Domain != "" && permissions.MatchPattern(rule.Domain, domain) {
			granted = true
			break
		}
	}

	// Extract sandbox_only flag
	sandboxOnly, _ := runtimectx.SandboxOnly(ctx)

	// ONLY strictly enforce the YAML limits for Plugins (AOT).
	// Tools (JIT) bypass this and proceed to PermManager.
	if sandboxOnly && !granted {
		return s.writeError(ctx, m, fmt.Errorf("security violation: plugin strictly forbidden from fetching %q", url))
	}

	if !s.PermManager.CheckNetworkAccess(domain) {
		return s.writeError(ctx, m, fmt.Errorf("security violation: network access to %q blocked by global overrides", domain))
	}

	s.Logger.Printf("HOST OP [fetch_url]: %s", url)

	result, err := s.fetchURL(url)
	if err != nil {
		return s.writeError(ctx, m, err)
	}

	return s.writeStringToMemory(ctx, m, result)
}

func (s *StatefulShell) fetchURL(url string) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	// Simple HTML stripping
	re := regexp.MustCompile(`<script.*?>.*?</script>|<style.*?>.*?</style>|<[^>]+>`)
	text := re.ReplaceAllString(string(body), "")

	// Cleanup extra whitespace
	reWhitespace := regexp.MustCompile(`\n\s*\n`)
	text = reWhitespace.ReplaceAllString(text, "\n")

	if len(text) > 15000 {
		return text[:15000] + "\n...[TRUNCATED]", nil
	}
	return text, nil
}

func (s *StatefulShell) hostStartProcess(ctx context.Context, m api.Module, cmdPtr, cmdLen uint32) uint32 {
	cmdBytes, _ := m.Memory().Read(cmdPtr, cmdLen)
	command := string(cmdBytes)

	perms, ok := runtimectx.Permissions(ctx)
	if !ok {
		return s.writeError(ctx, m, fmt.Errorf("security violation: missing granular permissions"))
	}

	granted := false
	for _, prefix := range perms.Shell {
		if strings.HasPrefix(command, prefix) {
			granted = true
			break
		}
	}

	// Extract sandbox_only flag
	sandboxOnly, _ := runtimectx.SandboxOnly(ctx)

	// ONLY strictly enforce the YAML limits for Plugins (AOT).
	if sandboxOnly && !granted {
		return s.writeError(ctx, m, fmt.Errorf("security violation: plugin strictly forbidden from starting process %q", command))
	}

	s.Logger.Printf("HOST OP [start_process]: %s", command)

	result, err := s.startProcess(command)
	if err != nil {
		return s.writeError(ctx, m, err)
	}

	return s.writeStringToMemory(ctx, m, result)
}

func (s *StatefulShell) containerWorkingDir() string {
	return s.sandboxManager.containerWorkingDir(s)
}

func (s *StatefulShell) containerExecEnv() []string {
	return s.sandboxManager.containerExecEnv(s)
}

func (s *StatefulShell) containerExecOutput(ctx context.Context, command string) (string, error) {
	return s.sandboxManager.containerExecOutput(s, ctx, command)
}

func (s *StatefulShell) startProcess(command string) (string, error) {
	return s.processManager.startProcess(s, command)
}

func (s *StatefulShell) hostReadProcessLogs(ctx context.Context, m api.Module, idPtr, idLen uint32) uint32 {
	idBytes, _ := m.Memory().Read(idPtr, idLen)
	id := string(idBytes)

	s.Logger.Printf("HOST OP [read_process_logs]: %s", id)

	result, err := s.readProcessLogs(id)
	if err != nil {
		return s.writeError(ctx, m, err)
	}

	return s.writeStringToMemory(ctx, m, result)
}

func (s *StatefulShell) readProcessLogs(id string) (string, error) {
	return s.processManager.readProcessLogs(s, id)
}

func (s *StatefulShell) hostKillProcess(ctx context.Context, m api.Module, idPtr, idLen uint32) uint32 {
	perms, ok := runtimectx.Permissions(ctx)
	sandboxOnly, _ := runtimectx.SandboxOnly(ctx)

	// ONLY strictly enforce the YAML limits for Plugins (AOT).
	// Verify they have at least *some* shell access defined in their YAML.
	if sandboxOnly && (!ok || len(perms.Shell) == 0) {
		return s.writeError(ctx, m, fmt.Errorf("security violation: plugin lacks strict granular permissions for shell access to kill process"))
	}

	idBytes, _ := m.Memory().Read(idPtr, idLen)
	id := string(idBytes)

	s.Logger.Printf("HOST OP [kill_process]: %s", id)

	result, err := s.killProcess(id)
	if err != nil {
		return s.writeError(ctx, m, err)
	}

	return s.writeStringToMemory(ctx, m, result)
}

func (s *StatefulShell) killProcess(id string) (string, error) {
	return s.processManager.killProcess(s, id)
}

func (s *StatefulShell) killBackgroundProcessLocked(ctx context.Context, process *BackgroundProcess) error {
	return s.processManager.killBackgroundProcessLocked(s, ctx, process)
}

func (s *StatefulShell) stopAllBackgroundProcesses(ctx context.Context) {
	s.processManager.stopAllBackgroundProcesses(s, ctx)
}

func (s *StatefulShell) readPathArg(m api.Module, ptr, length uint32) (string, error) {
	pathBytes, ok := m.Memory().Read(ptr, length)
	if !ok {
		return "", fmt.Errorf("failed to read path from memory")
	}

	return s.resolvePath(string(pathBytes))
}

func (s *StatefulShell) resolvePath(path string) (string, error) {
	return s.pathResolver.resolvePath(s, path)
}

func (s *StatefulShell) writeError(ctx context.Context, m api.Module, err error) uint32 {
	s.Logger.Printf("HOST ERROR RESPONSE: %v", err)
	return s.writeStringToMemory(ctx, m, "error: "+err.Error())
}

// writeStringToMemory is a helper to write a string back to Wasm memory.
// It requires an exported "malloc" function from the Wasm module.
func (s *StatefulShell) writeStringToMemory(ctx context.Context, m api.Module, str string) uint32 {
	strBytes := []byte(str)
	length := uint64(len(strBytes))

	allocMem := m.ExportedFunction("alloc_mem")
	if allocMem == nil {
		// fallback to standard malloc if tinygo didn't use our alias
		allocMem = m.ExportedFunction("malloc")
	}

	if allocMem == nil {
		s.Logger.Printf("HOST ERR [wasm_memory]: alloc_mem/malloc not exported from wasm module")
		return 0
	}

	results, err := allocMem.Call(ctx, length)
	if err != nil || len(results) == 0 {
		s.Logger.Printf("HOST ERR [wasm_memory]: alloc_mem call failed: %v", err)
		return 0
	}

	ptr := uint32(results[0])
	if !m.Memory().Write(ptr, strBytes) {
		s.Logger.Printf("HOST ERR [wasm_memory]: failed to write string to memory")
		return 0
	}

	// For a real implementation, we'd probably want to return an encoded uint64 (ptr << 32 | len)
	// but sticking to uint32 ptr for simplicity as per requirement example. The Wasm module
	// would need to know the length, perhaps via null termination or a separate function.
	// Let's assume null-termination for this simple example.
	if !m.Memory().Write(ptr+uint32(length), []byte{0}) {
		s.Logger.Printf("HOST ERR [wasm_memory]: failed to write string terminator to memory")
		return 0
	}

	return ptr
}

func (s *StatefulShell) hostGetState(ctx context.Context, m api.Module) uint32 {
	pluginName, ok := runtimectx.PluginName(ctx)
	if !ok || pluginName == "" {
		return s.writeStringToMemory(ctx, m, "")
	}

	paths := runtimeapi.Paths{
		WorkspaceDir: s.WorkspaceDir,
		StateDir:     s.StateDir,
	}
	statePath := filepath.Join(paths.PluginStateDir(), pluginName+".json")
	data, err := os.ReadFile(statePath)
	if err != nil {
		return s.writeStringToMemory(ctx, m, "") // Return empty string if no state exists yet
	}

	return s.writeStringToMemory(ctx, m, string(data))
}

func (s *StatefulShell) hostSetState(ctx context.Context, m api.Module, ptr, length uint32) uint32 {
	pluginName, ok := runtimectx.PluginName(ctx)
	if !ok || pluginName == "" {
		return s.writeError(ctx, m, fmt.Errorf("security violation: missing plugin context for state writing"))
	}

	dataBytes, ok := m.Memory().Read(ptr, length)
	if !ok {
		return s.writeError(ctx, m, fmt.Errorf("failed to read state data from memory"))
	}

	paths := runtimeapi.Paths{
		WorkspaceDir: s.WorkspaceDir,
		StateDir:     s.StateDir,
	}
	stateDir := paths.PluginStateDir()
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		return s.writeError(ctx, m, fmt.Errorf("failed to create state directory: %v", err))
	}

	statePath := filepath.Join(stateDir, pluginName+".json")
	if err := os.WriteFile(statePath, dataBytes, 0644); err != nil {
		return s.writeError(ctx, m, fmt.Errorf("failed to write state file: %v", err))
	}

	return s.writeStringToMemory(ctx, m, "success")
}
