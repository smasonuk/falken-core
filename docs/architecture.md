# Architecture

## System Shape

Falken is split into a small public API and a larger internal runtime:


- `pkg/falken`
  Public embedding facade. Hosts interact with the runtime through `Session`, config types, event types, and interaction interfaces.

- `pkg/pluginsdk`
  Shared helper package for Wasm tools/plugins.

- `pkg/workspacepath`
  Small path helper package used by filesystem-related code.

- `internal/agent`
  Streaming model loop, history management, long-term memory, todo/task stores, delegated sub-agents, verification runs, plan mode, and built-in host tools.

- `internal/host`
  Workspace snapshotting, Docker sandbox startup, command execution, diff generation/application, background process management, and Wasm host exports.

- `internal/permissions`
  `.falken.yaml` config plus file/shell/network policy enforcement.

- `internal/network`
  Permission-aware MITM proxy used by the sandbox.

- `internal/extensions`
  Loading of embedded and external Wasm tools/plugins, manifest parsing, and Wazero resource ownership.

- `internal/runtimeapi`
  Shared event, interaction, and path types used between the runtime and hosts.

- `internal/runtimectx`
  Context propagation for tool name, plugin name, permissions, event channels, and sandbox-only execution.

- `internal/tasks`
  Persisted delegated-task store.

- `internal/todo`
  Persisted todo/checklist store.

## Layers

The codebase intentionally separates three concerns.

### 1. Host layer

The host owns the real workspace  (../falken-term/) and all human-facing control points.


### 2. Runtime layer

The runtime owns agent execution and extension orchestration.

Key files:

- `pkg/falken/session.go`
- `internal/agent/*`
- `internal/extensions/*`

Responsibilities:

- resolve workspace/state paths
- load permissions config
- load tools and plugin manifests
- maintain chat history and structured memory
- stream completions from the model
- execute tool calls
- emit typed runtime events

### 3. Isolation and policy layer

The isolation layer ensures the agent works against a controlled environment instead of mutating the real repository directly.

Key files:

- `internal/host/snapshot.go`
- `internal/host/shell.go`
- `internal/host/patch.go`
- `internal/network/proxy.go`
- `internal/permissions/*`

Responsibilities:

- create a temporary workspace snapshot
- exclude or stub protected files
- start the Docker sandbox
- mount cache directories and proxy certs
- enforce file/shell/network permissions
- copy reviewed changes from the snapshot back to the real workspace

## Core Session Object

`pkg/falken.Session` is the public runtime facade. It owns:

- resolved `runtimeapi.Paths`
- the `agent.Runner`
- the `host.StatefulShell`
- the `permissions.Manager`
- the `network.SandboxProxy`
- loaded tools
- loaded plugin manifests/hooks
- Wazero resource sets for tools and plugins

Lifecycle:

1. `NewSession(...)`
   Resolves paths, loads permissions, constructs the shell, loads tools/plugins, and creates the runner.
2. `Start(ctx)`
   Starts the proxy, resets transient runtime state, creates a workspace snapshot, and launches the sandbox container.
3. `Run(ctx, prompt)`
   Streams the agent loop for one prompt and forwards translated runtime events to the host.
4. `GenerateDiff()`
   Diffs the real workspace against the sandbox snapshot.
5. `ApplyDiff(diff)`
   Copies approved changes from the snapshot back into the real workspace with guardrails.
6. `Close(ctx)`
   Shuts down the proxy, container, and Wazero resources.

## Agent Execution Model

`internal/agent.Runner` keeps:

- `ToolRegistry`
  All known tools, including inactive Wasm tools.
- `ActiveTools`
  The subset currently exposed to the model.
- `History`
  The working conversation window plus persisted log file.
- stores for memory, todos, and delegated tasks

The runner supports multiple modes:

- `default`
  Normal coding mode.
- `plan`
  Mostly read-only exploration mode. The only allowed write is the internal implementation plan via `write_plan`.
- `verify`
  Read plus command execution only.
- `explore`
  Read-only delegated-subagent mode.

`plan` is user-visible in the TUI. `verify` and `explore` are primarily used for delegated tasks and QA verification.

## Extension Model

Tools and plugins are both Wasm-backed, but they play different roles.

- Wasm tools become callable function tools for the model.
- Plugins are loaded as hook definitions plus declared permissions and are mainly surfaced today for approval metadata and host display.

Important nuance:

- the runtime fully supports tool execution today
- plugin manifests are loaded and aggregated today
- `WasmHook.Run(...)` exists, but the main session startup/run path does not currently dispatch plugin hook events automatically

That means plugin discovery and approval are real, while general hook orchestration is still a thinner layer than the tool system.

