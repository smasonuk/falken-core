# Falken Core Developer Overview

This document explains the structure and behavior of the supplied `falken-core` code. It is intended for developers who need to understand how the runtime fits together before delving into individual packages or making changes.

## 1. What this codebase provides

Falken Core is a Go library for running an agent session against a workspace. It provides:

* A public `pkg/falken` API for creating, starting, running, and closing sessions.
* A provider-neutral agent loop that sends messages and tools to an LLM.
* Built-in tools for reading files, mutating files safely, executing commands, and managing implementation plans.
* A managed file service with workspace-bound path resolution, read-before-write tokens, backups, rollback behavior, and basic secret rejection.
* A policy layer for file, shell, and network access decisions, including approval handling.
* Runtime execution support for local shell commands or sandbox-backed shell commands.
* Conversation-scoped state for history, memory, todos, plans, and command evidence.
* Extension surfaces for host-owned tools, hook providers, LLM adapters, sandbox runtime providers, and network policy/proxy adapters.

The core design theme is that high-risk operations are routed through narrow managed services instead of allowing arbitrary direct access. File writes go through the managed file service. Shell commands go through shell-write detection and policy. Tool availability is filtered by runtime mode and safety metadata. State writes use atomic persistence helpers.

## 2. Major package responsibilities

### `pkg/falken`

This is the public API surface. Hosts should import this package rather than internal packages.

Important files:

* `agent_simple.go`: simple `NewAgent` wrapper for local, in-memory embedded-agent usage.
* `session.go`: public session construction, lifecycle, run locking, state reset, and public type definitions.
* `session_runtime.go`: internal runtime assembly for policy, command execution, file operations, background processes, sandbox adapters, and network adapters.
* `session_agent.go`: bridges the public LLM and event interfaces to the internal agent runner.
* `session_tools.go`: registers built-in tools plus host-provided tools; creates scoped `ToolHost` values for provider tools.
* `session_builtins.go`: adapts internal built-in tools into the public session tool registry.
* `tool_host.go`: implements the policy-gated host services exposed to external tools.
* `tools.go`: public tool, hook, and host contracts.
* `runtime_provider.go`: public interface for injecting sandbox and network runtime adapters.
* `paths.go`: exposes canonical workspace and state paths.
* `project_approvals.go`: persists and merges project-scoped policy approvals.
* `memory.go`, `todos.go`, and the plan helpers in `session_agent.go`: expose conversation-state APIs.
* `doc.go`: package-level user-facing documentation.

### `internal/agent`

This package owns the provider-neutral agent loop and runtime context.

Important files:

* `runner.go`: orchestrates LLM completion, tool exposure, tool execution, events, history persistence, and implementation submission nudges.
* `model.go`: defines provider-neutral messages, tool calls, tool results, events, completion requests, and completion responses.
* `history.go`: assembles the system prompt from base prompt, mode, memory, todos, and prior history.
* `mode.go`: implements Default mode and Plan mode, including tool filtering.
* `runner_routing.go`: handles automatic Plan-mode routing.
* `runner_submission.go`: requires implementation submission when active plans or todos exist.

### `internal/builtintools`

This package is the registry and compatibility layer for the built-in tools exposed to the model. Implementations live in domain subpackages, with shared helper types in `internal/builtintools/api`.

Important files:

* `filetools/read_file.go`, `filetools/read_files.go`: managed file reads.
* `filetools/write_file.go`, `filetools/edit_file.go`, `filetools/multi_edit.go`, `filetools/apply_patch.go`, `filetools/delete_file.go`: managed file mutations.
* `filetools/glob.go`, `filetools/grep.go`: workspace discovery tools.
* `commandtools/execute_command.go`: policy-gated shell command execution.
* `planningtools/read_plan.go`, `planningtools/write_plan.go`, `planningtools/read_todos.go`, `planningtools/write_todos.go`: runtime plan and todo state.
* `planningtools/read_command_evidence.go`, `planningtools/submit_plan_implementation.go`: command evidence and implementation completion.
* `memorytools/read_memory.go`, `memorytools/update_memory.go`: conversation memory state.
* `registry.go`: registration order and lookup.
* `api/host.go`: per-tool access to runtime services.
* `api/schema.go`: helpers for constructing JSON schemas.
* `api/result.go`: helpers for normalizing tool execution results.
* `api/tool.go`: built-in tool interface and descriptor model.

### `internal/runtimefiles`

This package is a runtime-facing facade over the lower-level managed file service in `internal/files`.

It defines request/result structs that are convenient for tools and agent payloads, delegates operations to `files.Service`, and adapts lower-level result types into normalized JSON-friendly shapes.

### `internal/files`

This package is the core managed file service.

Important files:

* `service.go`: safe reads, token issuance, writes, deletes, backups, atomic workspace writes, secret scanning, and low-level symlink-safe mutation helpers.
* `edit.go`: exact and fuzzy targeted edits, multi-edit grouping, multi-edit rollback.
* `patch.go`: unified git-style patch parsing, planning, preflight checks, commit, and rollback.

This is one of the most important packages for safety and correctness. All workspace file mutations are intended to enter through `Service` methods rather than direct writes.

### `internal/workspace`

This package safely resolves paths inside the workspace boundary.

* `root.go`: normalizes a workspace root.
* `resolve.go`: resolves existing paths and create targets while rejecting lexical escapes, symlink escapes, UNC paths, and invalid current working directories.

### `internal/runtimeexec`

This package owns command execution state and command executors.

Important files:

* `state.go`: workspace-rooted execution state, current working directory, environment merging, sandbox environment, artifact roots.
* `executor.go`: local shell executor.
* `sandbox_executor.go`: sandbox-backed command executor.
* `background.go`: background process manager.
* `output.go`: command output truncation and artifact persistence.

### `internal/policy` and `internal/policy/runtime`

The policy package is the canonical access-control engine.

* `internal/policy/policy.go`: policy config, rule matching, approvals, strict allowlists, session/project approvals.
* `internal/policy/runtime/runtime.go`: runtime-facing evaluator that combines policy decisions with shell-write detection.
* `internal/policy/commands/shellwrite.go`: shell-write bypass detector.

### `internal/extensions`

This code includes extension metadata and registry logic.

* `internal/extensions/manifest/manifest.go`: strict JSON parsing and validation for Wasm tool/plugin manifests.
* `internal/extensions/tools/registry.go`: validated tool metadata registry.
* `internal/extensions/tools/activation.go`: runtime active-tool registry.
* `internal/extensions/tools/discovery.go`: filesystem discovery of tool manifests.

The public package documentation states that actual Wasm execution is outside core, but the internal manifest and metadata logic remains available.

### `internal/state`, `internal/store`, and `internal/persist`

These packages manage durable and conversation-scoped state.

* `internal/state/layout.go`: canonical layout for workspace-specific state directories.
* `internal/state/init.go`: creates required state directories.
* `internal/state/metadata.go`: reads, writes, and touches state metadata.
* `internal/state/reset.go`: resets conversation-scoped state while preserving durable state.
* `internal/store/*.go`: typed stores for history, memory, todos, plan, and command evidence.
* `internal/persist/files.go`: trusted atomic read/write helpers for state and artifact files.

## 3. Runtime lifecycle

The public entry point is usually:

```go
session, err := falken.New(falken.Config{...})
err = session.Start()
result, err := session.Run(ctx, falken.RunRequest{Prompt: "..."})
err = session.Close()
```

### Construction

`falken.New` calls `NewSessionFromConfig`, which requires an LLM. It converts public `Config` into an internal `SessionConfig` and calls `newSessionWithConfig`.

`newSessionWithConfig` is the constructor that resolves and stores:

* Canonical workspace root.
* Canonical state layout.
* Conversation stores for history, memory, todos, plan, and command evidence.
* Initial lifecycle state: `lifecycleNew`.
* Initial runner: `noopRunner` until `Start` assembles real resources.

It rejects NUL bytes in workspace or state paths and uses `workspace.NormalizeRoot` plus `state.ResolveLayout` to construct paths.

### Start

`Session.Start` is the main assembly step. It enforces lifecycle transitions:

* `new` → `starting` → `started` on success.
* `starting` → `new` on failure, allowing retry.
* Already started returns nil.
* Closed, starting, and closing states return deterministic errors.

Start performs these steps:

1. Ensures state directories with `state.EnsureLayoutState`.
2. Touches metadata with `state.TouchMetadata`.
3. Creates a session runtime:

   * If a `RuntimeProvider` is configured, uses `newAdapterSessionRuntime`.
   * If no runtime provider is configured, local execution is only allowed when `ExecutionDetails.Mode == ExecutionModeLocal`.
   * Sandbox execution without a runtime provider is rejected.
4. Starts runtime resources: network policy endpoint, network proxy, sandbox runtime.
5. Starts the tool hub with built-ins and external providers.
6. Starts the hook hub.
7. Creates shared `agent.ModeState` backed by the plan store.
8. Creates the agent runner if an LLM is configured.
9. Runs session-start hooks.
10. Stores assembled resources and marks the session started.

On startup failures after resource creation, cleanup is attempted and errors are joined where appropriate.

### Run

`Session.Run` enforces at most one top-level run at a time. It checks lifecycle state, sets `runActive`, delegates to the configured runner, and clears `runActive` afterward.

The real runner is usually `sessionAgentRunner`, which wraps `internal/agent.Runner`. It combines config-level and request-level event sinks and completion callbacks.

### Close

`Session.Close` refuses to close while a top-level run is active. Otherwise, it transitions to closing, releases resources, and finally marks the session closed. Cleanup order is:

1. Run session-close hooks.
2. Close hook providers.
3. Close tool providers.
4. Close runtime resources.

The runtime stops background processes before closing sandbox and network adapters.

### Reset conversation state

`ResetConversationState` clears only conversation-scoped files and recent artifact/truncation directories:

* History.
* Memory.
* Todos.
* Plan.
* Command evidence.
* Recent truncation output.
* Recent artifacts.

It preserves durable metadata and backups. It also resets agent mode to Default and rebuilds file operations so read tokens are cleared.

## 4. State layout

`state.ResolveLayout` builds a `Layout` with canonical paths:

* `StateRoot`
* `metadata.json`
* `conversations/current/history.json`
* `conversations/current/memory.json`
* `conversations/current/todos.json`
* `conversations/current/plan.json`
* `conversations/current/command_evidence.json`
* `conversations/current/verification.json` for legacy migration compatibility
* `cache/`
* `backups/`
* `truncations/`
* `truncations/recent/`
* `artifacts/`
* `artifacts/recent/`
* `plugins/`

If no explicit state root is provided, the default root is OS-specific:

* macOS: `~/Library/Application Support/falken/state/workspaces/<workspace-id>`
* Windows: `%LOCALAPPDATA%/Falken/State/workspaces/<workspace-id>` or config-dir fallback.
* Other platforms: `~/.local/state/falken/workspaces/<workspace-id>` unless `XDG_STATE_HOME` is set.

The workspace ID is based on a slug of the workspace basename plus a hash of the workspace root. This avoids collisions between workspaces with the same directory name.

State persistence uses `internal/persist`, which writes trusted state files atomically by writing a temp file, syncing it, renaming it into place, and syncing the parent directory.

Important distinction: `persist` is for trusted state/artifact paths, not policy-managed workspace mutations. Workspace writes should go through `internal/files.Service`.

## 5. Agent loop

The agent loop lives in `internal/agent.Runner`.

### Inputs

A runner is configured with:

* `LLM`: provider-neutral model interface.
* `History`: history/memory/mode/todo manager.
* `Mode`: current mode state.
* `Tools`: active tool provider.
* `Executor`: tool executor.
* `BaseSystemPrompt`: host-provided base instructions.
* `MaxToolRounds`: guard against infinite tool loops; defaults to 100.

### Run flow

`Runner.Run` does the following:

1. Calls `HistoryManager.PrepareRun` to build and persist the next message list.
2. Repeatedly fetches active tools.
3. Filters tools based on current mode.
4. Calls the LLM, using streaming if the LLM implements `StreamingLLM`.
5. Emits assistant text events as needed.
6. Persists assistant messages and tool call metadata.
7. If there are no tool calls and no active implementation plan/todos require submission, completes the run.
8. If active implementation state requires submission, appends a nudge asking the model to call `submit_plan_implementation`; a second attempt to finish without an accepted submission fails with `ErrImplementationSubmissionRequired`.
9. Otherwise executes each tool call, emits tool events, persists tool results, and loops.
10. If tool calls exceed the max round count, fails with `ErrMaxToolRoundsExceeded`.

### Tool-call validation

Before a tool is executed, `handleToolCall` validates:

* Tool call ID is non-empty.
* Tool name is non-empty.
* Tool arguments are valid JSON, normalizing empty args to `{}`.
* Tool is active.
* Tool is allowed in current mode.
* Tool executor is available.

Errors become structured `ToolResult` values rather than panics.

### Streaming behavior

If the configured LLM implements `StreamingLLM`, streamed chunks are emitted as assistant text events. The final `CompletionResponse` remains authoritative for persisted assistant messages and tool calls. If the final response lacks `AssistantText` but chunks were streamed, the streamed text is used as assistant text.

## 6. Runtime context in the system prompt

`HistoryManager.PrepareRun` constructs the system prompt from:

1. Base system prompt.
2. Current mode section.
3. Memory section.
4. Todo section, when todo support is configured.

It then appends the new user prompt, applies the configured compactor, persists the resulting messages, and returns a clone.

The mode section is rendered by `RenderMode`. Plan mode adds the restriction:

> Plan mode may read workspace files and may mutate Falken internal conversation state through explicitly plan-safe host-state tools. Do not execute commands, access the network, or mutate workspace files.

Memory is persisted as ordered text entries. Todos are persisted as structured items and rendered as:

* `[ ]` pending
* `[>]` in progress
* `[x]` completed

## 7. Runtime modes and tool filtering

There are two modes:

* `default`: active tools are allowed unless other policy blocks them at execution time.
* `plan`: read-only except for plan-safe conversation state tools.

`ModeState` tracks the current mode and has a `PlanManager` for plan persistence. Automatic Plan-mode routing can run before the main model turn when `PlanRouting` is heuristic or LLM-backed.

### Entering Plan mode

`EnterPlan` initializes the plan with `# Plan\n\n` if no plan exists, then switches mode to Plan.

### Exiting Plan mode

`ExitPlan` validates the current plan with the same implementation-plan validator used by public `Session.WritePlan`.

### Filtering tools in Plan mode

`FilterTools` calls `IsToolAllowed` for each active tool. In Plan mode, a tool is blocked if it:

* Declares workspace mutation capability.
* Declares broad or mutating file permissions.
* Declares shell capability or shell permissions.
* Declares network capability or network permissions.
* Has a mutating or command-like name such as `write`, `create`, `edit`, `patch`, `delete`, `shell`, `command`, `exec`, `deploy`, etc.
* Declares no safety or permissions at all.

A tool is allowed when it declares `PlanSafe`, or when it declares no mutating, shell, or network capabilities and has usable safety metadata.

Built-in plan-safe tools include file reads, plan/todo tools, command-evidence tools, and memory tools. Mutating file tools and command execution are blocked in Plan mode.

## 8. Built-in tools

Built-in tools are registered in `internal/builtintools/registry.go` in this order:

1. `read_file`
2. `read_files`
3. `glob`
4. `grep`
5. `write_file`
6. `edit_file`
7. `multi_edit`
8. `apply_patch`
9. `delete_file`
10. `execute_command`
11. `read_plan`
12. `write_plan`
13. `read_todos`
14. `write_todos`
15. `read_command_evidence`
16. `submit_plan_implementation`
17. `read_memory`
18. `update_memory`

All built-ins return a `Descriptor` containing name, description, JSON schema, category, keywords, load flags, and safety metadata. `pkg/falken/session_builtins.go` converts those descriptors into internal `tools.Entry` values consumed by the agent.

### Built-in host

Each built-in receives a `builtintools.Host`, assembled per invocation. The host may provide:

* Runtime file operations.
* Command executor.
* Execution state.
* Agent mode state.
* Todo manager.
* Memory manager.
* Conversation state.
* Event sink.

Tools call `RequireFileOps`, `RequireExecutor`, `RequireMode`, `RequireTodos`, `RequireMemory`, or `RequireConversationState` to obtain dependencies. Missing services return `ErrHostUnavailable` instead of panicking.

### Result helpers

`Success`, `Fail`, and `ResultFromPayload` normalize tool results into `agent.ToolExecutionResult`:

* `Success` marshals payload and marks status `ok`.
* `Fail` creates a failed JSON payload with `success`, `status`, and `error`.
* `ResultFromPayload` uses caller-provided success/status/content and derives `Error` from payload `error` when unsuccessful.

### Schema helpers

`schema.go` contains helpers for JSON schema construction:

* `ObjectSchema`
* `StringProp`
* `IntegerProp`
* `BoolProp`
* `StringEnumProp`
* `ArrayProp`
* `ObjectProp`
* `StringMapProp`

Schemas are marshalled at startup through `MustSchema`, which panics only if the static schema is not JSON-marshalable.

## 9. Built-in file tools

The built-in file tools delegate to `internal/runtimefiles.Operations`, which delegates to `internal/files.Service`.

### `read_file`

Reads one workspace file through the managed file service.

Key behavior:

* Resolves path inside the workspace.
* Applies file read policy.
* Rejects directories.
* Supports optional `start_line` and `end_line`.
* Issues and records a read token for the canonical resolved path.

The content is returned both as the tool content and inside the structured payload.

### `read_files`

Reads multiple files in one call. Each file has the same request shape as `read_file`. The overall result is successful only when every file succeeds. Individual results are returned in the `files` array.

This is preferred over sequential reads when tracing related files.

### `glob`

Finds workspace files and directories matching a glob pattern.

Example:

```json
{"pattern":"**/*.go","path":"internal","limit":50}
```

Key behavior:

* Resolves the search root inside the workspace.
* Applies file read policy before returning matched paths.
* Supports `**` recursive matching.
* Skips hidden paths and common generated/vendor directories by default.
* Supports `include_dirs`, `include_files`, `include_hidden`, `include_ignored`, `ignore`, `limit`, `offset`, and path or modified-time sorting.
* Does not issue read tokens.

`glob` is a discovery tool. Before editing any returned file, the agent must still call `read_file` or `read_files`.

### `grep`

Searches workspace file contents with a regular expression.

Examples:

```json
{"regex":"func New.*Tool","target_paths":["internal"],"glob":"**/*.go","output_mode":"content","context":2}
```

```json
{"output_mode":"files_with_matches","regex":"TODO|FIXME","target_paths":["."]}
```

Key behavior:

* Resolves file and directory targets inside the workspace.
* Recurses through directory targets.
* Applies file read policy before scanning each file.
* Supports `content`, `files_with_matches`, and `count` modes.
* Supports glob filtering, case-insensitive search, context lines, limit/offset, and line truncation.
* Skips hidden paths and common generated/vendor directories by default.
* Skips binary files.
* Does not issue read tokens.

`grep` is also a discovery tool. Before editing any matched file, the agent must still call `read_file` or `read_files`.

### `write_file`

Creates or overwrites a workspace file.

Parameters include:

* `path`
* `content`
* `operation`: `create`, `overwrite`, or `create_or_overwrite`
* optional mode string such as `0644`
* optional `working_dir`

Key behavior:

* Runtime policy, not tool arguments, decides whether approval is required.
* Existing-file overwrites require a current read token.
* Creates do not require a prior read token if the file does not exist.
* Existing-file overwrites create backups.
* Content is scanned for obvious private keys and API-key-like tokens.
* Writes go through symlink-safe atomic mutation helpers.

### `edit_file`

Performs one targeted string replacement in an existing file.

Key behavior:

* Requires prior read token.
* Requires non-empty `old` string.
* Requires a unique match unless `replace_all` is true.
* Defaults to exact matching. Whitespace-insensitive and fuzzy matching run only when `match_strategy` requests them.
* Commits by calling `Write` with `overwrite`, so policy, backup, token, and secret checks still apply.

Optional non-exact matching has two strategies:

1. Whitespace-insensitive matching using a regex built from the search words.
2. Levenshtein matching for multi-line blocks with matching first and last anchors and similarity over 0.85.

When fuzzy matching is used, replacement indentation is adjusted to match the actual matched block.

### `multi_edit`

Applies multiple exact-string replacements atomically across one or more files.

Key behavior:

* Groups edits by canonical resolved file path.
* Edits within one file apply in array order.
* Each edit operates on the output of prior edits for that file.
* If a later file group fails after earlier groups were committed, earlier groups are rolled back.
* Rollback restores original file content and mode and refreshes read tokens.

This is the preferred tool for logically related edits, especially across multiple files or multiple locations in one file.

### `apply_patch`

Applies a unified git-format diff patch.

Key behavior:

* Requires `diff --git a/... b/...` format.
* Rejects the legacy `*** Begin Patch` envelope.
* Rejects binary patches, copy patches, and rename patches.
* Supports create, modify, and delete.
* Parses hunks and validates old/new hunk counts.
* Applies hunks to current content and validates exact context/removal matches.
* Performs preflight planning for every file.
* Prepares rollback entries before committing.
* Commits each file through managed `Write` or `Delete` operations.
* Rolls back previously committed patch changes if any later file fails.

Patch modifications and deletes call `Read` during patch preflight, which issues fresh read tokens for the files being patched. The tool description still instructs the model to read files before patching; the managed service itself also reads targets while planning patch changes.

### `delete_file`

Deletes a single workspace file.

Key behavior:

* Requires prior read token.
* Rejects directories.
* Applies file write policy.
* Creates a backup before deletion.
* Deletes through symlink-safe managed helpers.
* Forgets the read token after successful deletion.

## 10. Managed file service details

The managed file service is `internal/files.Service`. It is initialized with:

* Canonical workspace root.
* Real workspace root after resolving root symlinks.
* Runtime policy evaluator.
* Read-token registry scope ID.
* Optional backup root, configured through `NewServiceForLayout`.

### Read tokens

Read tokens enforce read-before-write behavior.

A token records:

* Token ID.
* Scope ID.
* Canonical path.
* Content hash.
* Size.
* Modification time.
* Issue time.

`TokenRegistry` stores the most recent token per canonical path. `ValidateCurrent` recomputes the token for the current file and compares the canonical path and content hash. Size and mtime contribute to the token ID but `sameFileVersion` currently compares path and content hash only.

Writes, edits, multi-edits, and deletes validate tokens before mutating existing files. This prevents silently overwriting changes made since the last managed read.

### Safe path resolution

`internal/workspace` performs two distinct kinds of resolution:

* `ResolveExisting`: for paths that must already exist. It checks lexical containment first, then evaluates symlinks and verifies the real path remains inside the real workspace root.
* `ResolveForCreate`: for paths that may not exist yet. It evaluates symlinks on existing parents only, then reconstructs the missing path under the resolved parent and verifies the final target remains inside the real workspace root.

Both reject empty paths, UNC-style paths, and paths outside the workspace.

### Atomic workspace writes

Workspace writes use `writeWorkspaceFileAtomicMode`, which is more defensive than a normal `os.Rename` flow:

* Requires a trusted root.
* Normalizes the path against both lexical and real roots.
* Opens parent directories through file descriptors using `openat` and `O_NOFOLLOW`.
* Creates parent directories when needed for create operations.
* Creates a random `.falken-write-*.tmp` temp file in the target directory.
* Writes, chmods, syncs, and closes the temp file.
* Commits by either hard-linking without replacement or renaming over a validated regular-file target.
* Syncs the parent directory after commit.

The no-replace path uses `Linkat` then `Unlinkat`, which avoids replacing an existing file. Overwrite validates the target is either absent or a regular file before rename.

### Backups

Existing-file overwrites and deletes call `backupExistingFile`. Backups are stored under:

`<backup root>/managed-writes/<timestamp>-<hash>/<workspace-relative-path>`

Backup files are written with mode `0600`. The backup mechanism reads the existing file through managed no-follow helpers, then writes it atomically under the backup root.

### Secret scanning

Before writes and patch-created/modified content are committed, `scanSecretContent` rejects content containing:

* PEM private key markers.
* OpenSSH private key markers.
* API-key-like strings starting with `sk-` or `ghp_` and length at least 24.

This is a simple guardrail rather than a full secret scanner.

### Commit uncertainty

Some low-level failures may happen after a mutation might have committed, especially around syncing or cleanup after commit. These are represented by `errMutationMayHaveOccurred` and surfaced as `commit_uncertain` statuses for writes or deletes. Result structs include `MutationMayHaveOccurred` so callers can avoid assuming rollback is safe or complete.

## 11. Patch handling in detail

Patch handling has three phases:

1. Parse.
2. Plan/preflight.
3. Commit with rollback.

### Parse

`parseUnifiedPatch` requires a non-empty git-style unified diff. It rejects unsupported envelopes and expects each file to start with `diff --git`.

`parsePatchFile` handles:

* `---` and `+++` path markers.
* Hunk headers.
* Known git metadata such as `index`, file modes, similarity/dissimilarity.
* Rejection of rename/copy/binary patches.

Paths support `a/` and `b/` prefixes and `/dev/null`. Quoted patch paths are rejected.

`parsePatchHunk` parses hunk ranges using a regex and validates that actual ` `, `-`, and `+` lines match the old/new counts in the header. It also handles the `\ No newline at end of file` marker by trimming the previous line ending.

### Plan and preflight

`planPatch` walks each parsed file, classifies it as create/modify/delete/rename, and rejects duplicate resolved targets.

For each file:

* Create resolves the new path for creation, verifies it does not already exist, applies hunks to empty content, scans secrets, and evaluates create policy.
* Modify reads the existing target, applies hunks to current content, scans secrets, and evaluates write policy.
* Delete reads the existing target, applies hunks, verifies resulting content is empty, and evaluates write policy.
* Rename is classified but rejected as unsupported.

### Commit and rollback

Before committing, `preparePatchRollback` snapshots each target path, including existence, content, and mode.

Commit uses managed operations:

* Create → `Write` with `WriteOperationCreate`.
* Modify → `Write` with `WriteOperationOverwrite`.
* Delete → `Delete`.

If any file fails after earlier files committed, `rollbackPatch` restores committed entries in reverse order. Existing files are restored with `persist.WriteBytesAtomic` and original modes. Created files are removed. Read tokens are updated or forgotten accordingly. Empty parent directories created by a patch may be cleaned up.

## 12. Command execution

The built-in `execute_command` tool delegates to `runtimeexec.CommandExecutor`. There are two foreground executors:

* `LocalExecutor`: runs `/bin/sh -c <command>` locally.
* `SandboxExecutor`: delegates execution to a configured `SandboxRuntime`.

Both executors:

* Require an `ExecutionState`.
* Evaluate shell policy before execution.
* Resolve the working directory inside the workspace.
* Merge environment variables.
* Capture stdout, stderr, and combined output.
* Stream chunks through an optional stream sink.
* Truncate large output and store full output in an artifact file when needed.

### Built-in command guardrails

`ExecuteCommandTool` delegates enforcement to the runtime executor and evaluator:

* Shell-write patterns, such as redirection or `tee <file>`, are hard-blocked by runtime policy and cannot be approved.
* Risky deletion commands such as `rm`, `git clean`, and `find` are blocked from automatic execution when matched by approval-required shell rules.
* Model-facing tool schemas do not expose `approval_required`; approval is a runtime policy decision.

### Command result content

`buildCommandContent` renders a human-readable result with:

* Command.
* Status.
* Exit code.
* Error/policy/warning details.
* Truncation artifact note, if applicable.
* stdout and stderr sections.

`buildCommandPayload` returns structured data including policy outcome, shell-write bypass status, output summary, exit errors, cleanup errors, and immediate stdout/stderr for the active agent.

### Command Evidence

Every `execute_command` call is recorded as compact command evidence. Evidence stores command facts, bounded output metadata, shell-write bypass status, and policy outcome/explanation; it does not persist full command output. At plan submission time, a reviewer inspects recent command evidence and decides whether it plausibly includes reasonable verification.

### Output truncation

`runtimeexec` output collectors use `DefaultMaxInlineOutput` of 64 KiB unless overridden. If combined output exceeds the limit:

* Full combined output is written to `RecentArtifactRoot`.
* Inline stdout, stderr, and combined output are truncated by bytes.
* The result includes artifact path, original byte count, preview byte count, and inline limit.

If an artifact root is unavailable and output must be truncated, command execution returns an execution error.

## 13. Shell-write detection

`internal/policy/commands.HasShellWritePatterns` parses commands using `mvdan.cc/sh/v3/syntax` in Bash mode.

The detector returns whether direct shell file-write syntax was found, structured reasons, and any parse error. Parse errors are not hard-blocked by themselves; the runtime still asks the policy engine for a shell decision.

It detects:

* File-writing redirects such as `>`, `>>`, `<>`, `>|`, `&>`, and `&>>`.
* `tee` writes to file operands, including common wrappers such as `/usr/bin/tee`, `command tee`, and `env FOO=bar tee`.
* Shell writes inside command substitutions, process substitutions, subshells, blocks, and multi-statement commands.
* Static nested shell evaluation such as `sh -c 'echo hi > file'`, `bash -c 'cat foo >> bar'`, and `eval 'echo hi > file'`.

This detector is intentionally not a general command analyzer. Risky deletion commands are handled by approval-required shell rules in the policy engine.

## 14. Policy system

The policy engine in `internal/policy` evaluates file, shell, and network requests.

### Rule types

Policy config contains blocked, allowed, and project-approved rules for:

* Files.
* Shell commands.
* Network hosts.

Each rule uses a match kind:

* `exact`
* `prefix`
* `suffix`

File rules can optionally restrict modes:

* `read`
* `write`
* `create`

If a file rule has no modes, it matches all file modes.

### Decision order

For file, shell, and network requests, the decision order is broadly:

1. Blocked rule denies.
2. Session approval allows.
3. Project approval allows.
4. Allowed rule allows.
5. Strict allowlist denies if enabled.
6. If approval is required, ask the approval handler.
7. Otherwise allow by default.

Approval results can be:

* `once`
* `session`
* `project`
* `deny`

Session approvals are stored in memory. Project approvals are merged into policy config and persisted to `project_approvals.json` through a persister installed by `newSessionPolicyEngine`.

### Runtime policy evaluator

`internal/policy/runtime.Evaluator` wraps the core engine and adds runtime-specific interpretation.

For shell commands, it first runs shell-write detection. Shell-write bypasses are denied before asking the core engine. Other shell approval requirements come from the policy engine, including approval-required shell rules for risky commands such as `rm`, `git clean`, and `find`.

For file operations, create and write access automatically require runtime approval unless allowed or denied by policy rules. Model-facing tool arguments cannot downgrade that requirement.

It also maps core decisions into runtime-facing explanations and approval status values.

## 15. External tools and tool host

External host-owned tools are contributed through `ToolProvider`.

A provider implements:

```go
type ToolProvider interface {
    Start(context.Context, ToolHost) error
    Tools(context.Context) ([]ToolDescriptor, error)
    ExecuteTool(context.Context, ToolInvocation) (ToolExecutionResult, error)
    Close(context.Context) error
}
```

Optionally, a provider can implement `ScopedToolProvider`:

```go
type ScopedToolProvider interface {
    ExecuteToolWithHost(context.Context, ToolInvocation, ToolHost) (ToolExecutionResult, error)
}
```

Scoped providers receive a call-scoped `ToolHost` during execution, with capabilities restricted by the invoked tool’s `ToolSafety` metadata.

### Tool registration

`sessionToolHub.start` registers built-ins first, then each provider’s tools. It rejects duplicate tool names across all built-ins and providers.

Each public `ToolDescriptor` is validated with `ValidateToolDescriptor`:

* Name must be non-empty and match `^[a-zA-Z][a-zA-Z0-9_.-]*$`.
* Description must be non-empty.
* Parameters schema must be valid JSON object schema.

The descriptor is converted into an internal `tools.Entry` with safety metadata.

### Startup host vs scoped execution host

Provider `Start` receives a startup host created by `newProviderStartupToolHost`. This host is capability-scoped with no per-tool safety capabilities. The package documentation explains the intention: providers should not retain startup hosts to bypass per-tool safety.

During actual tool execution, scoped providers receive `newScopedToolHost`, which includes:

* Namespace for tool state.
* Tool-specific safety metadata.
* Combined event sinks for session and current run.

Non-scoped providers are supported for compatibility but do not receive per-run event sinks or host capabilities during execution.

### ToolHost services

`ToolHost` exposes:

* `WorkspaceRoot`
* `StateRoot`
* `CurrentWorkingDir`
* `Emit`
* `CheckFileAccess`
* `ExecuteCommand`
* `GetState`
* `SetState`

Capability checks are applied when `capabilityScoped` is true:

* File reads require `ReadsWorkspace` or `MutatesWorkspace`.
* File writes/creates require `MutatesWorkspace`.
* Commands require `ExecutesShell`.
* State requires `UsesHostState`.

`CheckFileAccess` resolves paths safely inside the workspace before policy evaluation. `ExecuteCommand` uses the session command executor and policy engine. State keys are hashed and stored under a namespace derived from provider/tool identity, preventing tools from choosing another provider’s state namespace.

## 16. Built-in tools and external tools in the agent

The agent sees one list of active tools from `sessionToolProvider`. That list is:

* Built-in tools.
* Provider tools registered by `sessionToolHub`.

Before each LLM turn, the agent asks for active tools and filters them by mode. This means tool exposure can change if the session enters or exits Plan mode.

When the LLM calls a tool:

1. The agent validates the tool call.
2. The agent checks mode policy again.
3. `sessionToolExecutor.ExecuteTool` dispatches to the hub.
4. Built-ins go through `executeBuiltinTool`.
5. Provider tools go through `ExecuteToolWithHost` when supported, otherwise `ExecuteTool`.
6. Results are translated back to internal `agent.ToolExecutionResult` and then to a persisted `ToolResult` message.

## 17. Plan tools and plan persistence

The runtime plan is not a workspace file. It is stored in conversation state through `PlanStore`, backed by `plan.json` path even though the content is plain text written with `persist.WriteTextAtomic`.

### `read_plan`

Reads current plan text from `ModeState.Plan()`. If empty, content returned to the model is `(no plan has been written yet)`, while payload includes the raw empty `plan` and `empty: true`.

### `write_plan`

Writes or replaces the entire implementation plan and its initial todo list atomically. The tool accepts `plan` and `todos`; `write_todos` is used later for progress updates.

The built-in `write_plan` validator uses the implementation-plan validator and also requires initial todos:

* Trimmed plan must be non-empty.
* Trimmed plan must be at least 100 bytes.
* Text must contain case-insensitive substrings for all required concerns:
  * `goal`
  * `file`
  * `step`
  * `verif`
* `todos` must contain at least one valid todo item.

The error messages describe the required sections as Goal, Files, Changes, and Verification. The keyword matching is intentionally broad so flexible headings can pass.

### `read_command_evidence`

Reads conversation-scoped command evidence recorded from `execute_command` calls. Evidence contains compact command facts, status, policy metadata, output metadata, review attempts, and the last command-evidence review. It does not store full command output.

### `submit_plan_implementation`

Submits the active plan/todo implementation for completion. The tool first requires all todos to be completed. It then asks the command-evidence reviewer whether recent commands plausibly verify the submitted work. A first insufficient or unclear review blocks completion; a second insufficient review is accepted with a warning. On acceptance, the tool clears the active plan and todos.

## 18. History, memory, todos, and compaction

### History

`HistoryStore` persists a JSON array of strings. Each string is itself a JSON-encoded `agent.Message`.

`HistoryManager.Load` decodes entries into `Message` values. `Append` reloads, appends, and writes the entire history atomically. `PrepareRun` also writes the prepared compacted message list.

### Memory

`MemoryStore` persists:

```json
{
  "current_goal": "...",
  "important_files": ["..."],
  "decisions": ["..."],
  "open_questions": ["..."],
  "entries": ["..."]
}
```

Legacy files containing only `entries` remain valid. Missing memory returns an empty state.

`MemoryManager` normalizes and validates durable memory:

* Trims strings.
* Drops empty list entries.
* Deduplicates lists in first-seen order.
* Limits each field to concise, prompt-safe sizes.

Structured memory renders compactly under `--- CURRENT AGENT MEMORY ---` with sections for current goal, important files, decisions, open questions, and notes. Fully empty memory renders `(no memory entries)`.

Built-in memory tools:

* `read_memory`: reads durable internal memory.
* `update_memory`: merge-updates memory without requiring callers to rewrite existing fields.

Example:

```json
{"current_goal":"Add glob, grep, and structured memory built-ins","add_important_files":["internal/builtintools/registry.go"]}
```

Memory is internal state, not a workspace file. Do not store secrets, raw code, large logs, or command output dumps.

### Todos

`TodoStore` persists structured todo items. The runtime `TodoManager` maps store items into `agent.Todo` values and supports backward-compatible fields:

* If `Content` is empty, it falls back to `Text`.
* If `Status` is empty, it derives status from `Done`.

Todos are validated for non-empty ID, non-empty content, known status, and duplicate IDs.

### Compaction

`HistoryManager` accepts a `MessageCompactor`, defaulting to `NoOpCompactor`. This is an explicit seam for future summarization or truncation policies. The no-op compactor returns defensive copies without changing order or content.

## 19. Runtime execution state

`runtimeexec.ExecutionState` tracks:

* Workspace root.
* Current working directory.
* Output artifact root.
* Session environment overrides.

It guarantees the current working directory remains inside the workspace and is a directory. Working-directory overrides for command execution are resolved relative to the current working directory.

Environment handling has two forms:

* `MergedEnvironment`: ambient OS environment plus session overrides plus request overrides. Used by local execution.
* `SandboxEnvironment`: deterministic base environment plus session overrides plus request overrides. Used by sandbox execution.

The default sandbox environment includes deterministic `HOME`, locale, terminal, and PATH values.

## 20. Runtime providers and sandbox execution

The public `RuntimeProvider` supplies optional adapters:

```go
type RuntimeProvider interface {
    NewRuntimeAdapters(context.Context, RuntimeAdapterRequest) (RuntimeAdapters, error)
}
```

Adapters can include:

* `SandboxRuntime`
* `NetworkProxy`
* `NetworkPolicy`

In sandbox mode, a `SandboxRuntime` is required. In local mode, commands use the local executor even when adapters exist.

`SandboxExecutor` evaluates shell policy, resolves working directory, creates stdout/stderr/combined stream buffers, and calls:

```go
SandboxRuntime.Execute(ctx, SandboxCommandRequest{...})
```

The sandbox runtime returns a low-level result with start, runtime, exit, and cleanup errors. The executor maps those into `CommandStatus` values.

If a network proxy is configured and exposes proxy endpoint values, `sessionRuntime.configureSandboxProxy` calls `SetProxy` on the sandbox runtime when it supports that optional method. Docker runtimes use this to inject managed proxy environment into containers. `remoteexec.Runtime` intentionally does not implement `SetProxy`; remote-runner command proxy environment is configured on the runner process/server.

## 21. Network policy integration

The public network policy surface is:

```go
type NetworkPolicyEvaluator interface {
    EvaluateNetworkPolicy(context.Context, NetworkPolicyEvaluationRequest) (NetworkPolicyEvaluationResult, error)
}
```

`networkPolicyEvaluatorAdapter` bridges runtime adapters to the internal runtime policy evaluator. Network requests are evaluated by host and approval requirement. The result contains allowed/denied and explanation.

Network request events are part of the public `Event` type, though the code shown here mainly defines the event shape and adapter interface. Actual proxy implementations live outside core.

## 22. Extension manifest and registry support

`internal/extensions/manifest` defines strict JSON models for tool and plugin manifests.

### Tool manifest

A tool manifest includes:

* `manifest_version`
* `name`
* `description`
* `runtime`
* `tools`
* optional permissions

Only manifest version `1` and runtime `wasm` are supported.

Each tool requires:

* Valid name.
* Non-empty description.
* JSON object input schema.

Tool names must start with a letter and contain only letters, numbers, dots, underscores, or hyphens. Duplicate tool names within one manifest are rejected.

### Plugin manifest

A plugin manifest is similar but declares hooks. Supported hook events in the manifest model include session, command, and file mutation hook points. The public `HookProvider` in `pkg/falken`, however, currently supports only `session_start` and `session_close` lifecycle hooks.

### Declared permissions

Manifests can request permissions for:

* Files.
* Network.
* Shell.

Permission match kinds must be exact, prefix, or suffix. File permission modes must be read, write, or create.

### Discovery and registration

`internal/extensions/tools/discovery.go` scans configured roots for package directories containing `tool.json`. Valid tool manifests are converted into `tools.Entry` values. Safety metadata is derived from declared permissions:

* File permissions imply `ReadsWorkspace`.
* File write/create permissions, or file permissions with no modes, imply `MutatesWorkspace`.
* Shell permissions imply `ExecutesShell`.
* Network permissions imply `UsesNetwork`.

The `Registry` stores validated entries by name, rejects duplicates, and returns deterministic name-sorted lists. `RuntimeRegistry` tracks which registered tools are active and prevents deactivation of `AlwaysLoad` tools.

## 23. Public static tools

`pkg/falken/static_tools.go` provides helpers for native Go tools:

* `ToolFunc`: wraps a descriptor and function into a `Tool`.
* `StaticToolProvider`: builds a `ToolProvider` over a fixed set of tools.

The provider validates descriptors and rejects duplicate static tool names. It defensively clones descriptors, invocations, arguments, and execution payloads to avoid accidental mutation across boundaries.

## 24. Events

Internal agent events include:

* `assistant_text`
* `tool_call`
* `tool_result`
* `command_chunk`
* `plan_routing_decision`
* `run_completed`
* `run_failed`
* `thought`

Public events also include `network_request` and `workspace_operation`.

Events are bridged between internal and public shapes in `session_agent.go` and `session_tools.go`. Command output streaming uses `runtimeexec.StreamChunk`, which is converted into public `CommandChunk` events.
Managed workspace file operations emit `workspace_operation` metadata without
file contents, patches, or edit strings.

`EventThought` is documented as best-effort only. Hosts should not depend on it for correctness.

## 25. Important call chains

### Session startup

```text
falken.New
  → NewSessionFromConfig
    → newSessionWithConfig
      → workspace.NormalizeRoot
      → state.ResolveLayout
      → newConversationStores

Session.Start
  → state.EnsureLayoutState
  → state.TouchMetadata
  → newAdapterSessionRuntime or newLocalOnlySessionRuntime
    → runtimeexec.NewExecutionStateForLayout
    → newSessionPolicyEngine
    → runtimepolicy.NewEvaluator
    → command executor setup
    → newSessionFileOperations
      → files.NewServiceForLayout
      → runtimefiles.NewOperations
  → runtime.start
  → sessionToolHub.start
  → sessionHookHub.start
  → agent.NewModeState
  → newSessionAgentRunner
  → hookHub.run(session_start)
```

### Agent run with tool call

```text
Session.Run
  → sessionAgentRunner.Run
    → agent.Runner.Run
      → HistoryManager.PrepareRun
      → ActiveTools
      → FilterTools
      → LLM Complete / StreamComplete
      → persist assistant message
      → handleToolCall
        → validate call ID/name/JSON
        → find active tool
        → IsToolAllowed
        → sessionToolExecutor.ExecuteTool
          → built-in or provider dispatch
      → persist tool result
      → repeat or complete
```

### Built-in file edit

```text
edit_file tool
  → host.RequireFileOps
  → runtimefiles.Operations.EditFile
  → files.Service.Edit
    → prepareEditFile
      → workspace.ResolveExisting
      → token validation
      → read current file content
    → applyExactEdit
      → exact match, or requested whitespace/fuzzy match
    → files.Service.Write(overwrite)
      → resolve target
      → policy evaluate
      → token validation
      → secret scan
      → backup
      → atomic write
      → record new token
```

### Shell command execution

```text
execute_command tool
  → host.RequireExecutor
  → runtimeexec.CommandExecutor.Execute
    → runtimepolicy.Evaluator.EvaluateShell
      → commands.HasShellWritePatterns
      → hard-block shell-write bypasses
      → policy.Engine.EvaluateShell
    → resolve working directory
    → execute local shell or sandbox runtime
    → stream/capture output
    → finalize/truncate output
  → build command content and payload
```

### Patch application

```text
apply_patch tool
  → runtimefiles.Operations.ApplyPatch
  → files.Service.ApplyPatch
    → planPatch
      → parseUnifiedPatch
      → classify each file
      → preflight path, content, secret, and policy checks
    → preparePatchRollback
    → for each planned file:
      → commitPatchFile
        → Write(create/overwrite) or Delete
      → on failure: rollbackPatch
```

## 26. Key safety invariants

A developer modifying this code should preserve these invariants:

1. Workspace paths must be resolved through `internal/workspace` before access.
2. Existing workspace mutations must require a current read token.
3. Workspace writes must go through `files.Service` and its atomic no-follow mutation helpers, not `persist`.
4. Existing-file overwrites and deletes must create backups.
5. Multi-file mutations should either fully apply or roll back prior committed files when possible.
6. Shell commands that write files through shell syntax must not bypass managed file mutation safeguards.
7. Risky deletion commands require approval-required shell rule handling.
8. Plan mode must not expose mutating, shell, or network-capable tools.
9. Public provider tools must be descriptor-validated and name-unique.
10. Capability-scoped tool hosts must enforce declared `ToolSafety`.
11. State/artifact persistence should remain atomic.
12. Session lifecycle must prevent concurrent top-level runs and resource shutdown while a run is active.

## 27. Error and status design

The code generally distinguishes between:

* Programming/configuration errors returned as Go errors.
* Expected runtime denials or failures returned as structured status results.

Examples:

* Invalid JSON tool arguments become `invalid_arguments` tool failures.
* Policy denial becomes a structured result with status such as `denied` or `blocked`.
* Unsafe paths become `unsafe_path` statuses.
* Missing read tokens become `missing_read_token` statuses.
* Stale read tokens become `stale_read_token` statuses.
* Unexpected filesystem errors may return Go errors.

This pattern lets the agent reason about expected operational failures without crashing the session, while still surfacing internal failures as errors.

## 28. Developer guide: where to make common changes

### Add a new built-in tool

Likely files:

* Add a new file in `internal/builtintools` implementing `Tool`.
* Add descriptor, schema, safety metadata, and execute method.
* Add the tool to `builtintools.All()` in `registry.go`.
* Ensure public conversion in `pkg/falken/session_builtins.go` does not need special handling.
* Add tests around descriptor validation, mode filtering, and execution.

Pay close attention to safety metadata. Plan mode relies heavily on it.

### Change file mutation behavior

Likely files:

* `internal/files/service.go`
* `internal/files/edit.go`
* `internal/files/patch.go`
* `internal/runtimefiles/operations.go`
* Corresponding built-in tool wrappers, if request/result shapes change.

Do not bypass `files.Service` with `persist` for workspace files. Preserve read-token validation, backups, policy checks, and symlink-safe operations.

### Add a new write operation status

Likely files:

* Lower-level status enum in `internal/files`.
* Adaptation logic in `internal/runtimefiles/operations.go`.
* Built-in result handling if success semantics change.
* Tests for status propagation and payload shape.

### Change command policy

Likely files:

* `internal/policy/commands/shellwrite.go` for shell-write bypass detection.
* `internal/policy/policy.go` for shell allow, deny, and approval-required rules.
* `internal/policy/runtime/runtime.go` for runtime interpretation.
* `internal/builtintools/commandtools/execute_command.go` for built-in command guardrails and payload rendering.

### Change Plan mode behavior

Likely files:

* `internal/agent/mode.go` for filtering.
* Built-in descriptors’ safety metadata.
* Public tool descriptors’ `ToolSafety` expectations.
* Tests around `FilterTools` and `IsToolAllowed`.

### Add or change persistent session state

Likely files:

* `internal/state/layout.go` for canonical paths.
* `internal/state/init.go` for directory creation.
* `internal/state/reset.go` for reset semantics.
* `internal/store` for typed store.
* `pkg/falken/paths.go` if public path exposure is needed.

Decide whether the state is conversation-scoped and should reset, or durable and should survive reset.

### Add a new public extension surface

Likely files:

* `pkg/falken/tools.go` or `runtime_provider.go` for public contracts.
* Session hub or runtime assembly code for lifecycle integration.
* Internal adapters for bridging public types into internal packages.
* Package documentation in `doc.go`.

Public API changes should preserve the package’s guidance that hosts do not import internal packages.

## 29. Notable implementation details and potential review points

These are not necessarily bugs, but they are useful things to know when reviewing or extending the code.

### `newSessionWithConfig`

The internal constructor is named `newSessionWithConfig`. It is marked deprecated and retained for compatibility/testing.

### Plan validation is shared

`agent.ValidatePlan`, public `Session.WritePlan`, `ExitPlanMode`, and built-in `write_plan` all use the implementation-plan validator. The built-in `write_plan` additionally requires an initial todo list and writes the plan and todos atomically.

### Patch preflight reads target files

`apply_patch` descriptions instruct the model to call `read_file` before patching, but `files.Service.planPatchModify` and `planPatchDelete` call `Read` internally during patch preflight. This means the service itself obtains fresh read tokens for patch targets. The model-facing rule still encourages review before mutation.

### `ReadToken` equality compares path and content hash

Token IDs include path, hash, size, and modtime, but `sameFileVersion` checks only path and content hash. This means same-content rewrites are treated as matching. That may be desired to prevent false stale-token failures, but it is a meaningful semantic choice.

### `persist` is trusted-state only

Several rollback paths use `persist.WriteBytesAtomic` to restore snapshots. That is internal rollback logic for already-resolved trusted paths. General workspace mutation paths should still use the managed file service.

### Plan mode blocks tools with no safety metadata

In Plan mode, an external tool with empty `Safety` and no permissions is blocked with `tool does not declare safety or permissions`. Providers should mark genuinely safe planning tools with `PlanSafe`.

### Workspace dirty tracking is heuristic

Successful `execute_command` calls are treated as potentially mutating unless they are simple commands from a small observational allowlist such as `ls`, `pwd`, `cat`, `head`, `tail`, `wc`, and similar. Compound commands and unrecognized executables are conservatively marked as possible workspace mutations.

### Built-in tools are always/default loaded

All built-ins shown here use both `AlwaysLoad: true` and `DefaultLoad: true`. External runtime registries also support activation/deactivation semantics, but built-ins are always present through the session tool hub.

## 30. Glossary

### Agent

The provider-neutral loop that builds messages, exposes tools, calls the LLM, executes tool calls, and persists results.

### Built-in tool

A native core tool in `internal/builtintools`, such as `read_file` or `execute_command`.

### External provider tool

A host-owned tool contributed through `ToolProvider` in the public API.

### Tool safety

High-level capability metadata used to decide Plan-mode exposure and scoped host permissions.

### Managed file service

The `internal/files.Service` responsible for safe workspace reads and mutations.

### Read token

A run/session-scoped record proving the caller read a specific version of a file before mutating it.

### Runtime policy evaluator

The layer that combines shell-write detection, policy rules, and approvals into execution decisions.

### Conversation state

Current history, memory, todos, and plan data. This can be reset without deleting backups or metadata.

### Durable state

State that survives conversation reset, such as metadata and backups.

### Plan mode

A read/planning mode where file mutations, command execution, and network-capable tools are blocked.

### Runtime provider

A host-supplied adapter provider that can create sandbox, network proxy, and network policy handles.

## 31. Suggested reading order for new developers

1. `pkg/falken/doc.go` for public API intent.
2. `pkg/falken/session.go` for lifecycle and public types.
3. `pkg/falken/session_runtime.go` for runtime assembly.
4. `pkg/falken/session_agent.go` and `internal/agent/runner.go` for the agent loop.
5. `internal/builtintools/registry.go` and individual built-in tools.
6. `internal/runtimefiles/operations.go` for tool-facing file operation payloads.
7. `internal/files/service.go`, `edit.go`, and `patch.go` for managed file safety.
8. `internal/workspace/resolve.go` for path safety.
9. `internal/policy/runtime/runtime.go`, `internal/policy/policy.go`, and `internal/policy/commands/shellwrite.go` for shell policy.
10. `pkg/falken/session_tools.go` and `tool_host.go` for external tool integration.
11. `internal/state`, `internal/store`, and `internal/persist` for persistence.

## 32. Mental model summary

A Falken session is a policy-gated, workspace-bound agent runtime.

The public `Session` owns lifecycle and resources. At startup it creates policy, execution state, command execution, managed file operations, tool and hook hubs, and the agent runner. During a run, the agent prepares prompt context, exposes mode-filtered tools to the LLM, executes requested tools through guarded dispatch paths, persists history, and emits events.

File operations are intentionally centralized. Reads issue tokens. Existing-file mutations require those tokens. Writes are backed up, policy-checked, secret-scanned, and committed atomically using symlink-resistant filesystem primitives. Multi-file operations attempt rollback on failure.

Commands are also centralized. They are parsed and classified, blocked from writing files through shell syntax, policy-checked, optionally approved, executed locally or in a sandbox, streamed to events, and truncated to artifacts when large.

Extension points exist, but they are routed through safety metadata and scoped hosts. Plan mode relies on that metadata to keep the runtime read-only except for plan updates.

When changing this code, the safest approach is to identify which managed boundary owns the behavior and modify that boundary rather than bypassing it.
