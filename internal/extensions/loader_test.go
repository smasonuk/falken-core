package extensions

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/smasonuk/falken-core/internal/extensions/manifest"

	"github.com/sashabaranov/go-openai/jsonschema"
	"github.com/tetratelabs/wazero"
)

func TestLoadTools_Success(t *testing.T) {
	tmpDir := t.TempDir()
	pluginDir := filepath.Join(tmpDir, "test_tool")
	os.MkdirAll(pluginDir, 0755)

	yamlContent := `name: test_tool
description: a test tool
requested_permissions:
  shell:
    - ls
tools:
  - name: test_tool
    description: a test tool
    parameters:
      type: object
      properties:
        cmd:
          type: string
`
	os.WriteFile(filepath.Join(pluginDir, "tool.yaml"), []byte(yamlContent), 0644)

	// Create a dummy wasm file
	wasmContent := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	os.WriteFile(filepath.Join(pluginDir, "main.wasm"), wasmContent, 0644)

	tools, resources, err := LoadTools(tmpDir, nil)
	if err != nil {
		t.Fatalf("LoadTools failed: %v", err)
	}
	defer resources.Close(context.Background())

	var wasmTool *WasmTool
	for _, tool := range tools {
		candidate, ok := tool.(*WasmTool)
		if ok && candidate.Name() == "test_tool" {
			wasmTool = candidate
			break
		}
	}
	if wasmTool == nil {
		t.Fatalf("Expected test_tool to be loaded, got %d tools", len(tools))
	}

	if wasmTool.Name() != "test_tool" {
		t.Errorf("Expected tool name 'test_tool', got '%s'", wasmTool.Name())
	}
}

func TestLoadPlugins_Success(t *testing.T) {
	tmpDir := t.TempDir()
	pluginDir := filepath.Join(tmpDir, "test_plugin")
	os.MkdirAll(pluginDir, 0755)

	yamlContent := `name: test_plugin
description: a test plugin
permissions:
  shell:
    - echo
hooks:
  - name: test_hook
    event: on_startup
`
	os.WriteFile(filepath.Join(pluginDir, "plugin.yaml"), []byte(yamlContent), 0644)

	// Create a dummy wasm file
	wasmContent := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	os.WriteFile(filepath.Join(pluginDir, "main.wasm"), wasmContent, 0644)

	hooks, resources, err := LoadPlugins(tmpDir, nil)
	if err != nil {
		t.Fatalf("LoadPlugins failed: %v", err)
	}
	defer resources.Close(context.Background())

	var found bool
	for _, h := range hooks {
		if h.PluginName == "test_plugin" {
			found = true
			break
		}
	}

	if !found {
		t.Fatalf("Expected test_plugin to be loaded")
	}
}

func TestEmbeddedExtensionsFSContainsManifest(t *testing.T) {
	// Check for a tool or plugin in embedded_assets
	// Based on sync_embedded_assets we should have tools and plugins subfolders

	entries, err := fs.ReadDir(EmbeddedExtensionsFS, "embedded_assets/tools")
	if err != nil {
		t.Fatalf("expected embedded_assets/tools to exist: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("expected embedded_assets/tools to not be empty")
	}
}

func TestEmbeddedManifestsMatchSource(t *testing.T) {
	sourceTools, err := filepath.Glob(filepath.Join("..", "..", "extensions", "tools", "*", "tool.yaml"))
	if err != nil {
		t.Fatalf("glob source tools: %v", err)
	}
	sort.Strings(sourceTools)

	var embeddedTools []string
	toolEntries, err := fs.ReadDir(EmbeddedExtensionsFS, "embedded_assets/tools")
	if err != nil {
		t.Fatalf("read embedded tools: %v", err)
	}
	for _, entry := range toolEntries {
		if entry.IsDir() {
			embeddedTools = append(embeddedTools, entry.Name())
		}
	}
	sort.Strings(embeddedTools)

	if len(sourceTools) != len(embeddedTools) {
		t.Fatalf("source/embedded tool manifest count mismatch: %d vs %d", len(sourceTools), len(embeddedTools))
	}

	for _, sourcePath := range sourceTools {
		name := filepath.Base(filepath.Dir(sourcePath))
		sourceData, err := os.ReadFile(sourcePath)
		if err != nil {
			t.Fatalf("read source manifest: %v", err)
		}
		embeddedData, err := fs.ReadFile(EmbeddedExtensionsFS, filepath.ToSlash(filepath.Join("embedded_assets/tools", name, "tool.yaml")))
		if err != nil {
			t.Fatalf("read embedded manifest for %s: %v", name, err)
		}
		if !bytes.Equal(sourceData, embeddedData) {
			t.Fatalf("embedded tool manifest for %s drifted from source", name)
		}
	}

	sourcePlugins, err := filepath.Glob(filepath.Join("..", "..", "extensions", "plugins", "*", "plugin.yaml"))
	if err != nil {
		t.Fatalf("glob source plugins: %v", err)
	}
	sort.Strings(sourcePlugins)

	var embeddedPlugins []string
	pluginEntries, err := fs.ReadDir(EmbeddedExtensionsFS, "embedded_assets/plugins")
	if err != nil {
		t.Fatalf("read embedded plugins: %v", err)
	}
	for _, entry := range pluginEntries {
		if entry.IsDir() {
			embeddedPlugins = append(embeddedPlugins, entry.Name())
		}
	}
	sort.Strings(embeddedPlugins)

	if len(sourcePlugins) != len(embeddedPlugins) {
		t.Fatalf("source/embedded plugin manifest count mismatch: %d vs %d", len(sourcePlugins), len(embeddedPlugins))
	}

	for _, sourcePath := range sourcePlugins {
		name := filepath.Base(filepath.Dir(sourcePath))
		sourceData, err := os.ReadFile(sourcePath)
		if err != nil {
			t.Fatalf("read source manifest: %v", err)
		}
		embeddedData, err := fs.ReadFile(EmbeddedExtensionsFS, filepath.ToSlash(filepath.Join("embedded_assets/plugins", name, "plugin.yaml")))
		if err != nil {
			t.Fatalf("read embedded manifest for %s: %v", name, err)
		}
		if !bytes.Equal(sourceData, embeddedData) {
			t.Fatalf("embedded plugin manifest for %s drifted from source", name)
		}
	}
}

func TestLoadTools_InvalidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	pluginDir := filepath.Join(tmpDir, "bad_tool")
	os.MkdirAll(pluginDir, 0755)

	yamlContent := `name: bad_tool
description:
  - invalid
  - yaml
  - structure
`
	os.WriteFile(filepath.Join(pluginDir, "tool.yaml"), []byte(yamlContent), 0644)
	os.WriteFile(filepath.Join(pluginDir, "main.wasm"), []byte("dummy"), 0644)

	if _, _, err := LoadTools(tmpDir, nil); err == nil {
		t.Fatalf("expected LoadTools to fail on invalid yaml")
	}
}

func TestLoadTools_MissingRequiredFieldFails(t *testing.T) {
	tmpDir := t.TempDir()
	pluginDir := filepath.Join(tmpDir, "bad_tool")
	os.MkdirAll(pluginDir, 0755)

	yamlContent := `name: bad_tool
tools:
  - name: bad_tool
    description: desc
    parameters:
      type: object
`
	os.WriteFile(filepath.Join(pluginDir, "tool.yaml"), []byte(yamlContent), 0644)
	os.WriteFile(filepath.Join(pluginDir, "main.wasm"), []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}, 0644)

	if _, _, err := LoadTools(tmpDir, nil); err == nil {
		t.Fatalf("expected missing required field to fail")
	}
}

func TestLoadPlugins_MalformedPermissionBlockFails(t *testing.T) {
	tmpDir := t.TempDir()
	pluginDir := filepath.Join(tmpDir, "bad_plugin")
	os.MkdirAll(pluginDir, 0755)

	yamlContent := `name: bad_plugin
description: desc
permissions:
  network:
    - domain: example.com
      url: https://example.com
hooks:
  - name: bad_hook
    event: on_startup
`
	os.WriteFile(filepath.Join(pluginDir, "plugin.yaml"), []byte(yamlContent), 0644)
	os.WriteFile(filepath.Join(pluginDir, "main.wasm"), []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}, 0644)

	if _, _, err := LoadPlugins(tmpDir, nil); err == nil {
		t.Fatalf("expected malformed permission block to fail")
	}
}

func TestWasmTool_Definition(t *testing.T) {
	tool := &WasmTool{
		ToolDef: manifest.ToolDefinition{
			Name:        "test_def",
			Description: "desc",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"arg1": map[string]any{"type": "string"},
				},
				"required": []any{"arg1"},
			},
		},
	}

	def := tool.Definition()
	if def.Name != "test_def" {
		t.Errorf("Expected Name 'test_def', got '%s'", def.Name)
	}

	if def.Parameters == nil {
		t.Fatalf("Parameters is nil")
	}

	params, ok := def.Parameters.(jsonschema.Definition)
	if !ok {
		t.Fatalf("Expected Parameters to be jsonschema.Definition, got %T", def.Parameters)
	}

	if params.Type != jsonschema.Object {
		t.Errorf("Expected Type 'object', got '%v'", params.Type)
	}

	if len(params.Properties) != 1 {
		t.Errorf("Expected 1 property, got %d", len(params.Properties))
	}

	if _, ok := params.Properties["arg1"]; !ok {
		t.Errorf("Expected property 'arg1' not found")
	}
}

func compileEmptyWasmModule(t *testing.T) (wazero.Runtime, wazero.CompiledModule) {
	t.Helper()
	runtime := wazero.NewRuntime(context.Background())
	compiled, err := runtime.CompileModule(context.Background(), []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00})
	if err != nil {
		t.Fatalf("CompileModule failed: %v", err)
	}
	return runtime, compiled
}

func TestWasmTool_RunNilContextFallsBack(t *testing.T) {
	runtime, compiled := compileEmptyWasmModule(t)
	defer runtime.Close(context.Background())

	tool := &WasmTool{
		PluginName: "plugin",
		ToolDef: manifest.ToolDefinition{
			Name:        "tool",
			Description: "desc",
		},
		Compiled: compiled,
		Runtime:  runtime,
	}

	result, err := tool.Run(nil, map[string]any{})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if got := result["result"]; got != "" {
		t.Fatalf("expected empty result for empty wasm module, got %#v", got)
	}
}

func TestWasmTool_RunSucceedsWithInheritedContext(t *testing.T) {
	runtime, compiled := compileEmptyWasmModule(t)
	defer runtime.Close(context.Background())

	tool := &WasmTool{
		PluginName: "plugin",
		ToolDef: manifest.ToolDefinition{
			Name:        "tool",
			Description: "desc",
		},
		Compiled: compiled,
		Runtime:  runtime,
	}

	result, err := tool.Run(context.Background(), map[string]any{"x": "y"})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if got := result["result"]; got != "" {
		t.Fatalf("expected empty result for empty wasm module, got %#v", got)
	}
}

func TestWasmHook_RunNilContextFallsBack(t *testing.T) {
	runtime, compiled := compileEmptyWasmModule(t)
	defer runtime.Close(context.Background())

	hook := &WasmHook{
		PluginName: "plugin",
		HookDef: manifest.HookDefinition{
			Name:  "hook",
			Event: "on_startup",
		},
		Compiled: compiled,
		Runtime:  runtime,
	}

	result, err := hook.Run(nil, map[string]any{})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if got := result["result"]; got != "" {
		t.Fatalf("expected empty result for empty wasm module, got %#v", got)
	}
}
