# Extensions And Tools

Falken combines built-in Go host tools with Wasm-loaded tools and plugins.

## Tool Model

`internal/agent.Runner` keeps two tool collections:

- `ToolRegistry`
  Every known tool, including inactive Wasm tools.
- `ActiveTools`
  The subset currently exposed to the model.

The OpenAI tool list is built from `ActiveTools`.

## Built-in Host Tools

These tools are injected directly by `internal/agent.NewRunner(...)`:

- `execute_command`
  Run shell commands through `host.StatefulShell`.
- `search_tools`
  Search the local registry and dynamically activate matching Wasm tools.
- `agent`
  Launch an asynchronous delegated sub-agent.
- `TaskCreate`
- `TaskList`
- `TaskGet`
- `TaskUpdate`
  Manage the delegated-task store.
- `TodoWrite`
  Replace the persisted checklist.
- `enter_plan_mode`
- `exit_plan_mode`
  Manage plan mode.
- `update_memory`
  Update structured long-term memory.
- `submit_task`
  Hand completed work back to the host for review.

These are always active because they define the runtime's core operating model.

## Wasm Tools

Wasm tools are loaded from:

- embedded assets under `internal/extensions/embedded_assets/tools`
- external workspace folders under `tools/`

Each extension directory contributes a `tool.yaml` plus `main.wasm`.

The manifest describes:

- extension name and description
- one or more callable tools
- JSON-schema-like parameter definitions
- `always_load`
- keywords and category metadata
- requested permissions

`internal/extensions.LoadTools(...)` compiles the Wasm modules with Wazero and returns `WasmTool` instances.

## Dynamic Activation

If a tool manifest sets `always_load: false`, the tool is loaded into the registry but not exposed to the model initially.

Today that mainly applies to `fetch_url` from the embedded `web` extension. The model can discover it by calling `search_tools`.

This keeps the default prompt/tool surface smaller while preserving specialized capabilities.

## Embedded Extensions In This Repository

Current embedded tools:

- `filesystem`
  Provides:
  - `read_file`
  - `read_files`
  - `write_file`
  - `edit_file`
  - `multi_edit`
  - `apply_patch`
  - `glob`
  - `grep`
- `background`
  Provides:
  - `start_background_process`
  - `read_process_logs`
  - `kill_process`
- `web`
  Provides:
  - `fetch_url`

Current embedded plugin:

- `secret_scanner`
  Declares an `on_startup` hook and is marked internal.

## Wasm Execution Model

When a Wasm tool runs:

1. Falken marshals a JSON payload with tool name, args, cwd, and workspace root.
2. The payload is passed on stdin.
3. The sandbox snapshot is mounted into Wazero with path parity to the real workspace path.
4. Host functions are exposed under the `env` module.
5. The module executes.
6. Stdout is interpreted as JSON when possible and otherwise returned as a plain string result.

The same general mechanism exists for `WasmHook.Run(...)`.

## Host Functions Exposed To Wasm

`host.StatefulShell.ExportHostFunctions(...)` currently exports:

- shell execution
- URL fetching
- background process start/logs/kill
- plugin state get/set
- backup creation

Permission checks happen in these host functions and in the shell/proxy they call into.

## Plugins Versus Tools

Tools and plugins are different layers:

- tools are function tools that the model can call directly
- plugins are hook-like Wasm modules with declared permissions and host-visible metadata

Current state of plugin support:

- plugin manifests are discovered and validated
- plugin permissions are aggregated into `Session.PluginInfos()`
- the TUI can require approval for non-internal plugins
- `WasmHook.Run(...)` exists
- the normal session startup/run path does not currently dispatch plugin hook events automatically

So plugin discovery and approval are implemented today, while generalized hook orchestration is still lighter-weight than the tool system.

## Permission Semantics

Tool manifests carry `requested_permissions`, but tools are still allowed to trigger host permission prompts outside that declaration.

Plugins are stricter:

- plugin hook execution marks the run as sandbox-only
- host shell/network/file operations can hard-fail when a plugin exceeds its declared permissions

This is why plugin approval is treated as a stronger trust decision than ordinary tool use.

## Tool Output Truncation

Large structured tool outputs are truncated by the runner when they exceed the in-context threshold.

When that happens:

- the full payload is written under `.falken/cache/`
- the model sees a summarized result pointing to the spill file

This keeps the live conversation smaller without losing the full output on disk.
