# Falken Core v1 Runtime

This note describes the implemented v1 runtime as exposed through `pkg/falken`.

## Usage Flow

This document describes the advanced `New`/`Session` runtime path. For the
simple in-memory embedded-agent path, use `falken.NewAgent`.

Hosts assemble the runtime with `falken.Config`, then use the session lifecycle:

1. `falken.New(config)` validates required configuration and resolves canonical paths.
2. `Session.Start()` initializes state, metadata, policy, execution, built-in/native tool providers, lifecycle hooks, and the agent runtime.
3. `Session.Run(ctx, request)` executes the v1 agent loop with at most one active top-level run per session.
4. `Session.ResetConversationState()` clears conversation-scoped history, memory, todos, plan state, command evidence, and recent artifacts while preserving durable state.
5. `Session.Close()` releases session-owned runtime resources and rejects future starts/runs.

`New` performs construction and validation only. Runtime discovery and startup side effects happen in `Start`.

## Public Configuration

Required fields:

- `WorkspaceDir`: workspace root for runtime operations.
- `LLM`: host-provided model implementation.

Optional fields:

- `StateDir`: explicit state root. Empty uses the canonical default for the workspace.
- `ToolProviders`: host-provided native or adapter-backed tools. Wasm tools are provided by `falken-extra/wasmtools`.
- `BuiltinTools`: selects all built-ins, no built-ins, or a named built-in subset.
  Empty preserves the default built-in set.
- `AllowWorkspaceToolsInRemoteMode`: permits custom provider tools that declare
  workspace read or mutation capabilities when a runtime provider supplies
  remote workspace file operations. The default false rejects those tools so
  they cannot accidentally use the agent pod filesystem as the workspace.
- `HookProviders`: host-provided session lifecycle hooks.
- `ExecutionDetails`: optional command runtime configuration. Empty uses sandbox mode with image `falken-core-runtime:latest`, runtime binary `docker`, workspace mount `/workspace`, and shell `/bin/sh`. Local mode runs commands on the host and is intended for development/testing only.
- `Runtime`: optional sandbox, network proxy, and network policy adapter provider. Sandbox mode requires a runtime provider.
- `VerificationReviewerLLM`: optional separate model for command-evidence review. Nil uses `LLM`.
- `BaseSystemPrompt`: base system prompt. Empty is allowed.
- `PlanRouting`: automatic Plan-mode routing mode. Empty defaults to heuristic routing; `llm` uses an LLM routing call; `disabled` skips automatic routing.
- `Events`: session-level event sink. Nil is a no-op.
- `OnCompleted`: session-level completion callback. Nil is a no-op.
- `Policy`: file, shell, and network policy rules. Empty uses default policy behavior.
- `ApprovalHandler`: host approval callback. Nil denies approval-required operations.
- `StateBackendProvider`: optional non-file backend for conversation state.

Per-run `RunRequest.Events` and `RunRequest.OnCompleted` can override/augment session-level handlers for a single run.

## Events and Results

Stable v1 event types are:

- `assistant_text`: assistant text emitted by the agent loop.
- `tool_call`: tool call requested by the model before execution.
- `tool_result`: tool result returned to the model after execution or blocking.
- `command_chunk`: shell command stdout/stderr chunk when surfaced by runtime execution.
- `network_request`: network policy/proxy decision surfaced to hosts.
- `workspace_operation`: managed file operation metadata without file contents,
  patches, or edit strings.
- `plan_routing_decision`: automatic Plan-mode routing observability payload.
- `run_completed`: normal run completion.
- `run_failed`: terminal run failure.
- `thought`: best-effort diagnostic event. Hosts must not depend on it for correctness.

`RunResult` contains `FinalOutput`, `Completed`, and `Error`. Normal completion emits `run_completed` and invokes the optional completion callback. Terminal failure emits `run_failed` and does not invoke the completion callback.

## LLM Adapter Seam

The core runtime uses only the provider-neutral `falken.LLM` interface. Provider-specific SDKs should be wrapped outside the session and agent loop. The `pkg/falken/llm` adapter package supplies an intermediate provider shape and conversion helpers for messages, tools, tool calls, tool results, completion requests, and completion responses. This keeps LangChainGo, OpenAI-compatible, or other future adapters from leaking SDK types into core runtime code.

Assistant text streaming is optional. A provider can implement `falken.StreamingLLM`; the runner emits streamed text chunks as `assistant_text` events while still persisting exactly one final assistant message from the returned `CompletionResponse`.

## Managed Safety Behavior

The public session routes runtime file and command work through managed internal services:

- Paths are workspace-safe and canonicalized.
- Access is policy-gated.
- Existing-file writes require a managed read token.
- Read tokens include canonical path, content hash, size, modtime, device/inode where available, and run-scope metadata.
- Stale read tokens reject mutation before writing and are rechecked immediately before commit.
- `glob` and `grep` are read-only discovery tools and do not issue read tokens; agents must still call `read_file` or `read_files` before editing discovered files.
- Existing files are backed up before overwrite, edit, patch, or delete.
- Final content is secret-scanned before commit.
- Direct shell-write bypasses are hard-blocked by policy before execution and cannot be approved; risky deletion commands such as `rm`, `git clean`, and `find` are approval-gated by approval-required shell rules unless an explicit hard-deny rule blocks them.

## Modes

V1 exposes only:

- `default`: normal tool exposure.
- `plan`: read/planning-safe tool exposure plus plan, todo, command-evidence, and memory state support.

Plan mode restrictions are enforced in code. Automatic routing can enter Plan mode before implementation when the configured router decides a request needs a plan; hosts can also call `EnterPlanMode`, `WritePlan`, `WritePlanAndTodos`, `ReadPlan`, `ReadTodos`, and `ExitPlanMode` directly.

When an active implementation plan or todo list exists, the runner requires `submit_plan_implementation` before the final response. The submission tool checks completed todos and asks a command-evidence reviewer whether recent commands plausibly show verification. This is not a general hard verification gate for every run.

Memory tools read and merge-update Falken internal state, not workspace files. Keep memory concise and do not store secrets, raw code, large logs, or command output dumps.

## Extension Runtime

Core exposes neutral `ToolProvider`, `ScopedToolProvider`, and `HookProvider` contracts. It no longer discovers Wasm tool roots, plugin roots, or instantiates a Wasm runtime. Wasm manifest discovery and execution live in `falken-extra/wasmtools` and are registered through `ToolProviders`.

Hook support is intentionally narrow: `HookSessionStart` runs after successful startup and can fail `Session.Start`; `HookSessionClose` runs during cleanup and its errors are joined while cleanup continues.
