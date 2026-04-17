package extensions

import (
	"github.com/smasonuk/falken-core/internal/extensions/manifest"
	"github.com/smasonuk/falken-core/internal/host"

	"github.com/tetratelabs/wazero"
)

// WasmTool is a loaded tool manifest paired with its compiled Wasm module.
type WasmTool struct {
	PluginName  string
	Internal    bool
	ToolDef     manifest.ToolDefinition
	Permissions manifest.GranularPermissions
	WasmBin     []byte
	Runtime     wazero.Runtime
	Shell       *host.StatefulShell
	Compiled    wazero.CompiledModule
}

// WasmHook is a loaded plugin hook manifest paired with its compiled Wasm module.
type WasmHook struct {
	PluginName  string
	Description string
	Internal    bool
	HookDef     manifest.HookDefinition
	Permissions manifest.GranularPermissions
	WasmBin     []byte
	Runtime     wazero.Runtime
	Shell       *host.StatefulShell
	Compiled    wazero.CompiledModule
}
