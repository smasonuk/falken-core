# Runtime Flow

This document follows the main path from host startup to reviewed patch application.

## 1. Host Startup

The primary interactive host is the sibling `falken-term` module.

High-level startup sequence:

1. resolve workspace and state paths with `falken.NewPaths("", "")`
2. load `.falken.yaml`
3. optionally open `.falken/debug.log`
4. build the OpenAI-compatible client
5. create a session bridge for approvals and events
6. create a `falken.Session`
7. start the TUI program

`NewSession(...)` prepares the runtime, but it does not start the proxy or Docker sandbox yet.

## 2. Session Construction

`pkg/falken.NewSession(...)`:

- resolves `runtimeapi.Paths`
- creates expected state directories
- loads the permissions config if one was not provided
- constructs `permissions.Manager`
- constructs `host.StatefulShell`
- loads tools from embedded assets and workspace `tools/`
- loads plugins from embedded assets and workspace `plugins/`
- creates the `agent.Runner`

At this point the runtime knows about tools, plugin manifests, and state paths, but the sandbox is still inactive.

## 3. TUI Startup Routes

Before the chat screen appears, the TUI may walk through startup routes:

- Git preflight if the working tree is dirty
- Init wizard if no cache config exists
- Plugin approval if unapproved non-internal plugins are present
- Permission overview if enabled in config

After those checks, the host transitions to the boot route and starts the session.

## 4. Session Start

`Session.Start(ctx)` performs runtime boot:

1. start the sandbox proxy
2. reset transient runtime state directories
3. choose the sandbox image
4. ask `host.StatefulShell` to start the sandbox

`ResetRuntimeState(...)` recreates:

- `.falken/backups/`
- `.falken/truncations/`

Persistent conversation state such as history, memory, todos, and delegated tasks is intentionally left alone.

## 5. Sandbox Boot

`StatefulShell.StartSandbox(...)` then:

1. creates a temporary snapshot of the real workspace
2. excludes ignored directories such as `.git`, `node_modules`, `vendor`, and `.falken`
3. stubs blocked files inside the snapshot
4. mounts the snapshot into the container at the real workspace path
5. mounts configured cache directories
6. mounts the generated proxy certificate
7. injects proxy environment variables
8. starts the Docker container
9. captures container environment for later exec calls

The result is path parity: inside the container the agent sees the expected workspace path, but the contents come from the snapshot rather than the real repository.

## 6. Prompt Submission

When the user submits a prompt:

1. the host creates a cancellable context
2. `Session.Run(ctx, prompt)` starts the agent loop
3. runtime events are translated into public `falken.Event` values
4. the TUI updates the chat screen as those events arrive

The run is asynchronous from the UI point of view, so the TUI stays responsive while the model streams.

## 7. Agent Loop

`internal/agent.Runner.Run(...)` performs each turn by:

1. preparing history
2. compacting older history when needed
3. reloading memory from `.falken/memory.json`
4. choosing the tool list for the current mode
5. streaming a completion from the model
6. emitting thought/text/tool-call events as content arrives
7. appending the assistant message to history
8. executing any requested tool calls
9. appending tool results to history
10. repeating until the model stops without more tool calls

## 8. History And Memory

The runner persists chat history to `.falken/history.jsonl`.

Compaction behavior today:

- after history grows past 30 messages, older `read_file`, `read_files`, `grep`, `glob`, and `fetch_url` tool outputs are replaced with a truncation notice
- after history grows past 80 messages, older messages are dropped more aggressively and summarized into structured memory

Structured memory in `.falken/memory.json` stores:

- current goal
- important files
- decisions
- open questions
- active plan path
- recent summary

## 9. Tool Execution

Tool execution is split into two classes.

### Built-in host tools

These run directly in Go and are injected by the runner:

- command execution
- tool search/activation
- delegated sub-agents
- task store tools
- todo store tool
- memory tool
- plan-mode tools
- submit tool

### Wasm tools

These are loaded from embedded assets or `tools/` and executed under Wazero with host functions exported from `StatefulShell`.

## 10. Modes

The runner supports several modes that change which tools are visible:

- `default`
  normal execution
- `plan`
  mostly read-only planning mode
- `verify`
  read plus command execution only
- `explore`
  read-only delegated-subagent mode

The TUI exposes user-initiated plan mode. `verify` and `explore` are mainly used by delegated tasks internally.

## 11. Delegated Tasks

The `agent` tool launches a background sub-run:

1. create a delegated task record
2. create a nested state root under `.falken/runs/...`
3. start a sandbox for the sub-agent
4. load tools with a filtered profile
5. run the sub-agent asynchronously
6. write its output into `.falken/tasks/<id>/result.md`
7. run a verification sub-run
8. write verification output into `.falken/tasks/<id>/verify.md`
9. update task status to completed or failed

This gives the main agent asynchronous background work plus a built-in QA pass.

## 12. Permissions During Execution

During execution:

- file checks go through `permissions.Manager.CheckFileAccess(...)`
- shell checks go through `CheckShellAccess(...)`
- sandbox HTTP(S) goes through `SandboxProxy`, which uses `CheckNetworkAccess(...)`

If config or cached approvals do not already allow the action, the runtime asks the host through the interaction bridge.

## 13. Submission And Review

The runtime does not apply sandbox changes to the real workspace automatically.

Instead the agent is expected to call `submit_task`, which:

1. emits a `work_submitted` event
2. lets the host switch to diff review
3. lets the human inspect the patch before it touches the real repository

This review checkpoint is the main persistence safety barrier.

## 14. Applying Changes

When the host accepts a reviewed diff:

1. Falken compares the real workspace and sandbox snapshot
2. blocked-file changes are filtered out again
3. allowed files are copied back from the snapshot
4. empty directories may be cleaned up

This work lives in `internal/host/patch.go`.

## 15. Shutdown

`Session.Close(ctx)`:

- shuts down the proxy
- stops/removes the Docker container
- kills tracked background processes
- deletes the temporary snapshot
- closes Wazero resources for tools/plugins

Persistent state under `.falken/` remains unless the host explicitly calls `ResetConversationState()`.
