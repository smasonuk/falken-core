package extensions

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/smasonuk/falken-core/internal/extensions/manifest"
	"github.com/smasonuk/falken-core/internal/host"
	"github.com/smasonuk/falken-core/internal/runtimectx"

	"github.com/google/uuid"
	"github.com/sashabaranov/go-openai"
	"github.com/sashabaranov/go-openai/jsonschema"
	"github.com/tetratelabs/wazero"
	"gopkg.in/yaml.v3"
)

func parseToolManifest(path string, yamlData []byte) (manifest.ToolManifest, error) {
	var m manifest.ToolManifest
	dec := yaml.NewDecoder(bytes.NewReader(yamlData))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return manifest.ToolManifest{}, fmt.Errorf("invalid tool manifest %s: %w", path, err)
	}
	if err := m.Validate(); err != nil {
		return manifest.ToolManifest{}, fmt.Errorf("invalid tool manifest %s: %w", path, err)
	}
	return m, nil
}

func parsePluginManifest(path string, yamlData []byte) (manifest.PluginManifest, error) {
	var m manifest.PluginManifest
	dec := yaml.NewDecoder(bytes.NewReader(yamlData))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return manifest.PluginManifest{}, fmt.Errorf("invalid plugin manifest %s: %w", path, err)
	}
	if err := m.Validate(); err != nil {
		return manifest.PluginManifest{}, fmt.Errorf("invalid plugin manifest %s: %w", path, err)
	}
	return m, nil
}

func (w *WasmTool) Name() string {
	return w.ToolDef.Name
}

func (w *WasmTool) Description() string {
	return w.ToolDef.Description
}

func (w *WasmTool) IsLongRunning() bool {
	return false
}

func (w *WasmTool) Definition() openai.FunctionDefinition {
	// Fallback empty schema
	var params jsonschema.Definition
	params.Type = jsonschema.Object
	params.Properties = make(map[string]jsonschema.Definition)

	if w.ToolDef.Parameters != nil {
		// The easiest way to convert map[string]any to jsonschema.Definition
		// is to marshal it to JSON and unmarshal it back into the struct.
		b, err := json.Marshal(w.ToolDef.Parameters)
		if err == nil {
			_ = json.Unmarshal(b, &params)
		}
	}

	return openai.FunctionDefinition{
		Name:        w.ToolDef.Name,
		Description: w.ToolDef.Description,
		Parameters:  params,
	}
}

func (w *WasmTool) Run(ctx context.Context, args any) (map[string]any, error) {
	cwd := ""
	workspaceRoot := ""
	if w.Shell != nil {
		absCWD, err := filepath.Abs(w.Shell.RealCWD)
		if err == nil {
			cwd = absCWD
		} else {
			cwd = w.Shell.RealCWD
		}
		absWorkspaceRoot, err := filepath.Abs(w.Shell.WorkspaceDir)
		if err == nil {
			workspaceRoot = absWorkspaceRoot
		} else {
			workspaceRoot = w.Shell.WorkspaceDir
		}
	}
	payload := map[string]any{
		"command":        w.ToolDef.Name,
		"args":           args,
		"cwd":            cwd,
		"workspace_root": workspaceRoot,
	}

	argsJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	runCtx := ctx
	if runCtx == nil {
		runCtx = context.Background()
	}
	runCtx = runtimectx.WithToolName(runCtx, w.ToolDef.Name)
	runCtx = runtimectx.WithPluginName(runCtx, w.PluginName)
	runCtx = runtimectx.WithPermissions(runCtx, w.Permissions)

	var stdout, stderr wazeroPipe
	stdin := bytes.NewReader(argsJSON)

	var fsConfig wazero.FSConfig
	if w.Shell != nil && w.Shell.SandboxCWD != "" {
		// Path Parity: Mount the SandboxCWD directly to RealCWD.
		// Wazero's native WithDirMount securely prevents directory traversal escapes.
		fsConfig = wazero.NewFSConfig().WithDirMount(w.Shell.SandboxCWD, w.Shell.RealCWD)
	} else {
		fsConfig = wazero.NewFSConfig().WithDirMount("/", "/")
	}

	config := wazero.NewModuleConfig().
		WithName(w.ToolDef.Name + "-" + uuid.New().String()).
		WithStdin(stdin).
		WithStdout(&stdout).
		WithStderr(&stderr).
		WithFSConfig(fsConfig)

	module, err := w.Runtime.InstantiateModule(runCtx, w.Compiled, config)
	if module != nil {
		defer module.Close(runCtx)
	}

	if err != nil {
		return nil, fmt.Errorf("wasm execution failed: %v, stderr: %s", err, string(stderr.buffer))
	}

	var result map[string]any
	err = json.Unmarshal(stdout.buffer, &result)
	if err != nil {
		result = map[string]any{"result": string(stdout.buffer)}
	}

	return result, nil
}

func (w *WasmHook) Run(ctx context.Context, args any) (map[string]any, error) {
	cwd := ""
	if w.Shell != nil {
		absCWD, err := filepath.Abs(w.Shell.RealCWD)
		if err == nil {
			cwd = absCWD
		} else {
			cwd = w.Shell.RealCWD
		}
	}
	payload := map[string]any{
		"hook": w.HookDef.Name,
		"args": args,
		"cwd":  cwd,
	}

	argsJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	runCtx := ctx
	if runCtx == nil {
		runCtx = context.Background()
	}
	runCtx = runtimectx.WithToolName(runCtx, w.HookDef.Name)
	runCtx = runtimectx.WithPluginName(runCtx, w.PluginName)
	runCtx = runtimectx.WithPermissions(runCtx, w.Permissions)
	runCtx = runtimectx.WithSandboxOnly(runCtx, true)

	var stdout, stderr wazeroPipe
	stdin := bytes.NewReader(argsJSON)

	var fsConfig wazero.FSConfig
	if w.Shell != nil && w.Shell.SandboxCWD != "" {
		// Path Parity: Mount the SandboxCWD directly to RealCWD.
		// Wazero's native WithDirMount securely prevents directory traversal escapes.
		fsConfig = wazero.NewFSConfig().WithDirMount(w.Shell.SandboxCWD, w.Shell.RealCWD)
	} else {
		fsConfig = wazero.NewFSConfig().WithDirMount("/", "/")
	}

	config := wazero.NewModuleConfig().
		WithName(w.HookDef.Name + "-" + uuid.New().String()).
		WithStdin(stdin).
		WithStdout(&stdout).
		WithStderr(&stderr).
		WithFSConfig(fsConfig)

	module, err := w.Runtime.InstantiateModule(runCtx, w.Compiled, config)
	if module != nil {
		defer module.Close(runCtx)
	}

	if err != nil {
		return nil, fmt.Errorf("wasm execution failed: %v, stderr: %s", err, string(stderr.buffer))
	}

	var result map[string]any
	err = json.Unmarshal(stdout.buffer, &result)
	if err != nil {
		result = map[string]any{"result": string(stdout.buffer)}
	}

	return result, nil
}

type wazeroPipe struct {
	buffer []byte
	offset int
}

func (w *wazeroPipe) Read(p []byte) (n int, err error) {
	if w.offset >= len(w.buffer) {
		return 0, io.EOF
	}
	n = copy(p, w.buffer[w.offset:])
	w.offset += n
	return n, nil
}

func (w *wazeroPipe) Write(p []byte) (n int, err error) {
	w.buffer = append(w.buffer, p...)
	return len(p), nil
}

func LoadTools(toolDir string, shell *host.StatefulShell) ([]any, ResourceSet, error) {
	runCtx := context.Background()
	resources, err := newRuntimeResources(runCtx)
	if err != nil {
		return nil, nil, err
	}
	if shell != nil {
		if err := shell.ExportHostFunctions(runCtx, resources.runtime); err != nil {
			_ = resources.Close(runCtx)
			return nil, nil, err
		}
	}

	var allTools []any

	// Load embedded tools
	embeddedTools, err := loadToolsFromFS(EmbeddedExtensionsFS, "embedded_assets/tools", resources, shell, true)
	if err != nil {
		_ = resources.Close(runCtx)
		return nil, nil, err
	}
	allTools = append(allTools, embeddedTools...)

	// Load external tools
	if _, err := os.Stat(toolDir); err == nil {
		externalTools, err := loadToolsFromFS(os.DirFS(toolDir), ".", resources, shell, false)
		if err != nil {
			_ = resources.Close(runCtx)
			return nil, nil, err
		}
		allTools = append(allTools, externalTools...)
	}

	return allTools, resources, nil
}

func LoadPlugins(pluginDir string, shell *host.StatefulShell) ([]WasmHook, ResourceSet, error) {
	runCtx := context.Background()
	resources, err := newRuntimeResources(runCtx)
	if err != nil {
		return nil, nil, err
	}
	if shell != nil {
		if err := shell.ExportHostFunctions(runCtx, resources.runtime); err != nil {
			_ = resources.Close(runCtx)
			return nil, nil, err
		}
	}

	var allHooks []WasmHook

	// Load embedded plugins
	embeddedHooks, err := loadPluginsFromFS(EmbeddedExtensionsFS, "embedded_assets/plugins", resources, shell, true)
	if err != nil {
		_ = resources.Close(runCtx)
		return nil, nil, err
	}
	allHooks = append(allHooks, embeddedHooks...)

	// Load external plugins
	if _, err := os.Stat(pluginDir); err == nil {
		externalHooks, err := loadPluginsFromFS(os.DirFS(pluginDir), ".", resources, shell, false)
		if err != nil {
			_ = resources.Close(runCtx)
			return nil, nil, err
		}
		allHooks = append(allHooks, externalHooks...)
	}

	return allHooks, resources, nil
}

func loadToolsFromFS(fsys fs.FS, root string, resources *runtimeResources, shell *host.StatefulShell, isInternal bool) ([]any, error) {
	entries, err := fs.ReadDir(fsys, root)
	if err != nil {
		return nil, err
	}

	var tools []any
	runCtx := context.Background()

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		pluginRoot := filepath.ToSlash(filepath.Join(root, entry.Name()))
		yamlPath := pluginRoot + "/tool.yaml"
		wasmPaths := []string{
			pluginRoot + "/main.wasm",
			pluginRoot + "/build/main.wasm",
		}

		yamlData, err := fs.ReadFile(fsys, yamlPath)
		if err != nil {
			continue
		}

		manifest, err := parseToolManifest(yamlPath, yamlData)
		if err != nil {
			return nil, err
		}

		var wasmBin []byte
		for _, wasmPath := range wasmPaths {
			wasmBin, err = fs.ReadFile(fsys, wasmPath)
			if err == nil {
				break
			}
		}
		if err != nil {
			return nil, fmt.Errorf("tool %q missing wasm binary: %w", manifest.Name, err)
		}

		compiled, err := resources.runtime.CompileModule(runCtx, wasmBin)
		if err != nil {
			return nil, fmt.Errorf("tool %q failed to compile wasm: %w", manifest.Name, err)
		}
		resources.track(compiled)

		for _, toolDef := range manifest.Tools {
			tools = append(tools, &WasmTool{
				PluginName:  manifest.Name,
				Internal:    isInternal,
				ToolDef:     toolDef,
				Permissions: manifest.RequestedPermissions,
				WasmBin:     wasmBin,
				Runtime:     resources.runtime,
				Shell:       shell,
				Compiled:    compiled,
			})
		}
	}
	return tools, nil
}

func loadPluginsFromFS(fsys fs.FS, root string, resources *runtimeResources, shell *host.StatefulShell, isInternal bool) ([]WasmHook, error) {
	entries, err := fs.ReadDir(fsys, root)
	if err != nil {
		return nil, err
	}

	var hooks []WasmHook
	runCtx := context.Background()

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		pluginRoot := filepath.ToSlash(filepath.Join(root, entry.Name()))
		yamlPath := pluginRoot + "/plugin.yaml"
		wasmPaths := []string{
			pluginRoot + "/main.wasm",
			pluginRoot + "/build/main.wasm",
		}

		yamlData, err := fs.ReadFile(fsys, yamlPath)
		if err != nil {
			continue
		}

		manifest, err := parsePluginManifest(yamlPath, yamlData)
		if err != nil {
			return nil, err
		}

		var wasmBin []byte
		for _, wasmPath := range wasmPaths {
			wasmBin, err = fs.ReadFile(fsys, wasmPath)
			if err == nil {
				break
			}
		}
		if err != nil {
			return nil, fmt.Errorf("plugin %q missing wasm binary: %w", manifest.Name, err)
		}

		compiled, err := resources.runtime.CompileModule(runCtx, wasmBin)
		if err != nil {
			return nil, fmt.Errorf("plugin %q failed to compile wasm: %w", manifest.Name, err)
		}
		resources.track(compiled)

		for _, hookDef := range manifest.Hooks {
			hooks = append(hooks, WasmHook{
				PluginName:  manifest.Name,
				Description: manifest.Description,
				Internal:    isInternal,
				HookDef:     hookDef,
				Permissions: manifest.Permissions,
				WasmBin:     wasmBin,
				Runtime:     resources.runtime,
				Shell:       shell,
				Compiled:    compiled,
			})
		}
	}
	return hooks, nil
}
