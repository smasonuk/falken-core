# Falken Core

Falken Core is the v1 runtime library for creating a workspace-scoped Falken session. The public API lives in `pkg/falken`; hosts should not need to import `internal` packages.

## Public Usage

```go
session, err := falken.New(falken.Config{
    WorkspaceDir:     "/path/to/workspace",
    StateDir:         "/path/to/state", // optional
    ExecutionDetails: falken.ExecutionConfig{Mode: falken.ExecutionModeLocal}, // or provide Runtime for sandbox mode
    LLM:              myLLM,
    Events: func(event falken.Event) {
        // Stream assistant text, tool calls/results, command chunks, and run events
    },
    OnCompleted: func(ctx context.Context, result falken.RunResult) error {
        // Optional host notification after normal completion.
        return nil
    },
})
if err != nil {
    return err
}
if err := session.Start(); err != nil {
    return err
}
defer session.Close()

result, err := session.Run(ctx, falken.RunRequest{Prompt: "Help me update this project"})
```

The normal lifecycle is:

1. Build `falken.Config`.
2. Call `falken.New`.
3. Call `Session.Start`.
4. Call `Session.Run`.
5. Optionally call `Session.ResetConversationState`.
6. Call `Session.Close`.

## Configuration

`WorkspaceDir` and `LLM` are required. `StateDir` is optional; when omitted Falken uses the canonical state location for the workspace. Custom tools are supplied with `ToolProviders`; Wasm tools are supplied by `falken-extra/wasmtools`, not by core path discovery.

`ExecutionDetails` optionally customizes the command runtime. Empty execution config uses sandbox mode, image `falken-core-runtime:latest`, runtime binary `docker`, workspace mount `/workspace`, and shell `/bin/sh`.
`ExecutionDetails.Mode` defaults to `sandbox`; sandbox mode requires a `Runtime` provider. `local` mode is available for development/testing and runs commands on the host.

`Runtime` optionally provides sandbox, network proxy, and network policy adapters. `PlanRouting` controls automatic Plan-mode routing (`heuristic` by default, with `llm` and `disabled` options). `VerificationReviewerLLM` can provide a separate reviewer for command-evidence checks; nil defaults to `LLM`. `StateBackendProvider` can replace the default file-backed conversation stores.

`Events` and `OnCompleted` default to no-op behavior. `ApprovalHandler` is optional, but approval-required file, shell, and network operations are denied when no handler is configured. `Policy` can allow, block, or require approval for file, shell, and network access.

## Project Permissions

Project-scoped file, shell, and network permissions are saved in the active state root at `project_approvals.json`. Hosts can discover the exact path from `session.Paths.ProjectPermissionsPath`.

On the first launch for a workspace state directory, Falken writes permissive development defaults once:

- File: a `prefix` rule for the workspace root with `read`, `write`, and `create`.
- Shell: common development command prefixes such as `git `, `go `, `npm `, `python3 `, and `cargo `.
- Network: common source and package hosts such as `github.com`, `.github.com`, `proxy.golang.org`, `registry.npmjs.org`, `pypi.org`, and `crates.io`.

Defaults are only a bootstrap. If a user edits or deletes these rules and saves an empty permissions file, Falken will not restore defaults on the next launch.

Use `Session.ReadProjectPermissions` and `Session.WriteProjectPermissions` to load and save the editable project permissions model for a running session. Setup UIs can use `ReadProjectPermissionsForWorkspace` and `WriteProjectPermissionsForWorkspace` before session start.

```go
err := session.WriteProjectPermissions(falken.ProjectPermissions{
    Files: []falken.FileRule{{
        Path:  "/path/to/workspace",
        Match: falken.MatchPrefix,
        Modes: []falken.FileAccessMode{
            falken.FileAccessRead,
            falken.FileAccessWrite,
            falken.FileAccessCreate,
        },
    }},
    Shell: []falken.ShellRule{{
        Command: "go ",
        Match:   falken.MatchPrefix,
    }},
    Network: []falken.NetworkRule{{
        Host:  ".github.com",
        Match: falken.MatchSuffix,
    }},
})
```

Match modes are `exact`, `prefix`, and `suffix`. File modes are `read`, `write`, and `create`.

Shell permissions do not bypass runtime safety checks. Falken hard-blocks shell-write bypasses such as `echo > file`, `cat > file`, `tee file`, `printf >> file`, and heredocs to files because they bypass the managed file service. Risky deletion commands such as `rm`, `git clean`, and `find` are controlled by approval-required shell rules, so they do not run automatically but can proceed after host approval. Network rules are enforced only when the network policy/proxy path is enabled; otherwise the rules are saved but may not restrict normal outbound traffic.

## Library Smoke Test

```bash
go test ./...
```

`falken-core` is a library package; CLI entry points live outside this repository. Examples and most tests use `ExecutionModeLocal` for development-only command execution.

## LLM Provider Adapters

The core runtime depends only on the small `falken.LLM` interface. Provider SDK integrations live behind adapter packages outside core rather than inside the session or agent loop. `pkg/falken/llm` provides a provider-adapter seam and conversion helpers for messages, tool definitions, tool calls, tool results, completion requests, and completion responses.

Providers that can stream assistant text may implement `falken.StreamingLLM`. Streaming is optional: the final `CompletionResponse` remains authoritative for persisted assistant messages and tool calls, while streamed text chunks are emitted as `assistant_text` events.

## Events and Results

The public event stream uses stable v1 event types:

- `assistant_text`
- `tool_call`
- `tool_result`
- `command_chunk`
- `network_request`
- `plan_routing_decision`
- `run_completed`
- `run_failed`
- `thought`

`thought` is best-effort diagnostic output only. Hosts should use assistant/tool/run events for correctness.

`RunResult` reports the final assistant output, whether the run completed, and a terminal error string when the run failed. Recoverable tool execution problems are returned to the model as `tool_result` events/messages rather than forcing the whole run to fail.

## Safety Guarantees

Falken v1 keeps workspace mutation behind managed services:

- Workspace paths are canonicalized and checked before file operations.
- File, shell, and network access are policy-gated.
- Existing-file mutation requires prior managed read-token validation.
- Stale reads are rejected before mutation.
- Existing files are backed up before managed overwrite, edit, patch, or delete operations.
- Final file content is secret-scanned before commit.
- Shell-write bypass patterns such as direct output redirection are blocked before execution.

## Built-in Tools

Core built-ins include managed file tools, `execute_command`, plan/todo tools, command-evidence tools, and memory tools.
`glob` and `grep` are discovery tools: they are workspace-scoped and policy-gated, but they do not issue read tokens. Before editing any file found through search, call `read_file` or `read_files`.

Examples:

```json
{"pattern":"**/*.go","path":"internal","limit":50}
```

```json
{"regex":"func New.*Tool","target_paths":["internal"],"glob":"**/*.go","output_mode":"content","context":2}
```

```json
{"output_mode":"files_with_matches","regex":"TODO|FIXME","target_paths":["."]}
```

```json
{"current_goal":"Add glob, grep, and structured memory built-ins","add_important_files":["internal/builtintools/registry.go"]}
```

Memory is Falken internal state, not workspace content. Keep it concise and never store secrets, raw code, command output dumps, or large logs.

Plan and todo state is also conversation-scoped. `read_command_evidence` exposes compact command facts recorded from `execute_command`. `submit_plan_implementation` validates completed todos and asks a command-evidence reviewer whether recent commands plausibly verified the work; it clears the active plan/todos when accepted.

## V1 Scope

Falken v1 includes Default and Plan modes. Plan mode is enforced when entered and permits only read/planning-safe actions plus plan/todo/command-evidence state tools. Automatic plan routing can enter Plan mode before implementation, or hosts can disable routing.

Runs with active implementation plans or todos must submit through `submit_plan_implementation` before the final response. This is a lightweight conversation-state workflow, not a general runtime verification gate. Falken v1 intentionally does not include delegated jobs, delegated merge-back, Verify mode, Explore mode, or non-Wasm extension runtimes. Future seams are documented in [docs/deferred-capability-seams.md](docs/deferred-capability-seams.md).
