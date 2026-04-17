package extensions

// test needs reworking, there is no execute_command wasm tool any longer

// import (
// 	"context"
// 	"io"
// 	"log"
// 	"os"
// 	"os/exec"
// 	"path/filepath"
// 	"strings"
// 	"testing"

// 	"falken-core/internal/extensions/manifest"
// 	"falken-core/internal/host"
// 	"falken-core/internal/permissions"

// 	"github.com/tetratelabs/wazero"
// )

// func TestWasmExecuteCommand(t *testing.T) {
// 	// Compile the plugin using tinygo locally for the test
// 	// If tinygo is not available, we skip the test.
// 	// tinygoPath, err := exec.LookPath("tinygo")
// 	// if err != nil {
// 	// 	t.Skip("tinygo not installed, skipping wasm compilation test")
// 	// }

// 	tinygoPath := "/opt/homebrew/bin/tinygo"

// 	// Test if tinygo works, skip if not (e.g. go version mismatch)
// 	testCmd := exec.Command(tinygoPath, "version")
// 	testOut, testErr := testCmd.CombinedOutput()
// 	if testErr != nil || strings.Contains(string(testOut), "requires go version") {
// 		t.Skipf("tinygo not compatible with current go version, skipping test: '%s', '%s'", string(testOut), testErr.Error())
// 	}

// 	repoRoot := "../../"
// 	pluginSrc := filepath.Join(repoRoot, "internal", "tools", "source", "execute_command", "main.go")
// 	wasmOut := filepath.Join(repoRoot, "internal", "tools", "source", "execute_command", "main.wasm")

// 	cmd := exec.Command(tinygoPath, "build", "-o", wasmOut, "-target", "wasi", pluginSrc)
// 	cmd.Env = append(os.Environ(), "GOROOT=/usr/local/go", "GOTOOLCHAIN=go1.25.0")
// 	// cmd.Env = append()
// 	// Make sure we override the PATH so tinygo picks up the /usr/local/go first.
// 	for i, e := range cmd.Env {
// 		if strings.HasPrefix(e, "PATH=") {
// 			cmd.Env[i] = "PATH=/usr/local/go/bin:" + strings.TrimPrefix(e, "PATH=")
// 		}
// 	}
// 	out, err := cmd.CombinedOutput()
// 	if err != nil {
// 		if strings.Contains(string(out), "requires go version") {
// 			t.Skipf("tinygo not compatible with current go version, skipping test: '%s'", string(out))
// 		}
// 		t.Fatalf("failed to compile tinygo plugin: %v\nOutput: %s", err, string(out))
// 	}
// 	defer os.Remove(wasmOut)

// 	pm := permissions.NewManager(nil, nil)
// 	cwd, _ := os.Getwd()
// 	shell := host.NewStatefulShell(cwd, t.TempDir(), pm, log.New(io.Discard, "", 0))
// 	shell.TestingMode = true // Allow local execution for Wasm testing

// 	// Load the compiled plugin directly to WasmTool
// 	wasmBin, err := os.ReadFile(wasmOut)
// 	if err != nil {
// 		t.Fatalf("failed to read compiled wasm: %v", err)
// 	}

// 	runtime := wazero.NewRuntime(context.Background())
// 	compiled, err := runtime.CompileModule(context.Background(), wasmBin)
// 	if err != nil {
// 		t.Fatalf("failed to compile module: %v", err)
// 	}

// 	wasmTool := &WasmTool{
// 		ToolDef: manifest.ToolDefinition{
// 			Name: "execute_command",
// 		},
// 		WasmBin:  wasmBin,
// 		Runtime:  runtime,
// 		Shell:    shell,
// 		Compiled: compiled,
// 	}

// 	// Call the Wasm module with test input
// 	args := map[string]any{
// 		"Command": "echo",
// 		"Args":    []string{"hello", "wasm"},
// 	}

// 	result, err := wasmTool.Run(nil, args)
// 	if err != nil {
// 		t.Fatalf("WasmRun failed: %v", err)
// 	}

// 	if result == nil {
// 		t.Fatalf("Expected result, got nil")
// 	}

// 	outStr, ok := result["result"].(string)
// 	if !ok {
// 		t.Fatalf("Expected result to have string 'result' key, got %v", result)
// 	}

// 	if !strings.Contains(outStr, "hello wasm") {
// 		t.Errorf("Expected output to contain 'hello wasm', got: '%s'", outStr)
// 	}
// }
