# Embedding And State

This document covers the public embedding API in `pkg/falken` and the runtime state stored under `.falken/`.

## Public API Surface

The main entry point is `falken.Session`.

### Creating A Session

Create a session with `falken.NewSession(falken.Config{...})`.

Important config fields:

- `Client`
  Required OpenAI-compatible client.
- `ModelName`
  Model identifier used for chat completions.
- `SystemPrompt`
  Base runtime instructions.
- `WorkspaceDir`
  Workspace root. Defaults to the current working directory if omitted.
- `StateDir`
  State root. Defaults to `<workspace>/.falken`.
- `Logger`
  Optional logger.
- `PermissionsConfig`
  Optional preloaded permissions config. If omitted, the session loads `.falken.yaml`.
- `InteractionHandler`
  Optional host callback implementation for permissions, plan approval, and submit-for-review.
- `EventHandler`
  Optional sink for typed runtime events.
- `ToolDir` and `PluginDir`
  Optional overrides for external extension directories. Relative paths are resolved from the workspace.
- `SandboxImage`
  Optional Docker image override.
- `Debug`
  Reserved in the public config and used by hosts such as `falken-term`.

### Session Methods

- `Start(ctx)`
  Start the proxy, reset transient state, create the snapshot, and boot the Docker sandbox.
- `Run(ctx, prompt)`
  Execute one prompt through the agent runtime.
- `GenerateDiff()`
  Diff the real workspace against the sandbox snapshot.
- `ApplyDiff(diff)`
  Apply reviewed sandbox changes back to the real workspace.
- `Close(ctx)`
  Clean up proxy, sandbox, and extension resources.
- `Paths()`
  Return the resolved workspace and state paths.
- `ClearHistory()`
  Clear in-memory chat history.
- `ResetConversationState()`
  Remove in-memory and on-disk conversation state such as history, memory, todos, and delegated tasks.
- `ForcePlanMode(userInitiated bool)`
  Put the runner into plan mode before the next run.
- `PermissionsConfig()`
  Return the active permissions config.
- `ToolInfos()`
  Return loaded tool names for host UIs.
- `PluginInfos()`
  Return aggregated plugin metadata, including declared network/file/shell permissions.

## Interaction Handler

`InteractionHandler` is the host bridge for decisions that require user or host participation.

Methods:

- `RequestPermission`
  Called for file, shell, or network approvals.
- `RequestPlanApproval`
  Called when user-initiated plan mode wants approval to exit planning.
- `OnSubmit`
  Called when the runtime signals that work is ready for human review.

The TUI implements this with `SessionBridge`. The headless example in `examples/headless/main.go` shows a minimal embedding with a non-UI handler.

## Event Model

Hosts receive runtime activity through `EventHandler`.

Current public event types:

- `thought`
- `assistant_text`
- `tool_call`
- `tool_result`
- `command_chunk`
- `work_submitted`
- `run_completed`
- `run_failed`

This keeps host integrations decoupled from the internal agent message types.

## State Directory Layout

By default the state root is:

- `<workspace>/.falken`

Important files and directories:

- `history.jsonl`
  Persisted chat transcript log.
- `memory.json`
  Structured long-term agent memory.
- `todos.json`
  Persisted todo/checklist state.
- `tasks.json`
  Delegated-task index.
- `tasks/`
  Task-specific artifacts such as `result.md` and `verify.md`.
- `cache/`
  Transient runtime cache.
- `cache/proxy-ca.crt`
  Generated MITM proxy certificate trusted by the sandbox.
- `truncations/`
  Large-output spill files created when command or tool output is truncated.
- `backups/`
  First-touch file backups created by filesystem tooling.
- `plugin_states/`
  Per-plugin persisted state.
- `runs/`
  Nested state directories for delegated sub-runs and verification runs.
- `debug.log`
  Optional debug log when a host enables it.

## What Persists

Persisted across runs:

- chat log
- structured memory
- todos
- delegated task index and artifacts
- plugin state
- approved plugins and persisted permissions in `.falken.yaml`

Ephemeral per session:

- Docker container
- temporary workspace snapshot
- session approval cache
- live event channels
- proxy process
- background processes started inside the sandbox

## Memory Shape

`memory.json` currently stores:

- current goal
- important files
- architectural decisions
- open questions
- active plan path
- recent summary of compacted history
- last updated timestamp

This memory is merged into the system prompt before each model request.

## Conversation Reset

`ResetConversationState()` clears:

- in-memory history
- `history.jsonl`
- `memory.json`
- `todos.json`
- `tasks.json`
- `tasks/`

It does not remove `.falken.yaml`, plugin state, or the workspace itself.

## Headless Example

`examples/headless/main.go` demonstrates the smallest embedding path:

1. construct the API client
2. create a `falken.Session`
3. provide an `InteractionHandler`
4. call `Start(...)`
5. call `Run(...)`
6. call `Close(...)`

That example is useful because it shows that the TUI is just one host around the runtime, not the runtime itself.
