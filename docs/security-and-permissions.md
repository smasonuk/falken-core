# Security And Permissions

Falken is designed so the agent does not freely access or mutate the host environment.

## Security Model Summary

The main defenses are:

- work against a temporary snapshot instead of the real repository
- execute shell commands inside a Docker sandbox
- route sandbox HTTP(S) through a host-controlled proxy
- block or stub sensitive files
- require host-approved file, shell, and network access
- re-check blocked files when applying reviewed diffs

This is a host-mediated safety model, not "agent has direct filesystem control."

## Workspace Snapshotting

Before the sandbox starts, `internal/host/snapshot.go` creates a temporary copy of the workspace.

Important behaviors:

- ignored directories such as `.git`, `node_modules`, `vendor`, `.falken`, `.venv`, and `venv` are excluded or removed
- blocked files are replaced with stub content
- the sandbox writes into the snapshot, not the real repository

This means the real workspace is untouched until the host explicitly applies a reviewed diff.

## Default Blocked Files

The built-in deny list in `internal/permissions/config.go` includes:

- `.env`
- `.env.*`
- `*.pem`
- `*.key`
- `id_rsa`
- `id_rsa.pub`
- `secrets.json`
- `credentials.json`
- `*.p12`
- `.git/config`
- `.git/hooks/*`
- `.falken.yaml`
- `.falken/history*.jsonl`
- `.falken/debug.log`
- `.falken/cache/*`

These defaults prevent secret exposure, self-escalation, and tampering with runtime internals.

## `.falken.yaml`

Project config can define:

- `sandbox_image`
- `show_permission_overview`
- `caches`
- `global_blocked_urls`
- `global_allowed_urls`
- `global_blocked_files`
- `global_allowed_files`
- `global_blocked_commands`
- `global_allowed_commands`
- `persistent_allowed_urls`
- `persistent_allowed_commands`
- `persistent_allowed_files`
- `strict_file_allowlist`
- `strict_command_allowlist`
- `approved_plugins`

There is also legacy support for `project_dotfiles`, which is migrated into `persistent_allowed_files`.

## Permission Manager

`internal/permissions.Manager` is the central policy object.

Its three public decision paths are:

- `CheckFileAccess(...)`
- `CheckShellAccess(...)`
- `CheckNetworkAccess(...)`

Each path broadly follows this order:

1. explicit deny rules
2. explicit allow rules
3. session or persisted approvals
4. host prompt through `InteractionHandler`
5. optional caching/persistence of the approval

Approval scopes are:

- `once`
- `session`
- `project`
- `deny`

## File Access Rules

File access is path-based and uses glob-like matching.

Important current details:

- `global_blocked_files` and built-in blocked files always win
- `global_allowed_files` can short-circuit approval
- `persistent_allowed_files[path]` can allow `read` or `read/write`
- the session cache also stores read vs read/write grants
- `.falken/state.md` is specially exempted as an agent scratchpad
- strict file allowlist behavior only activates when `strict_file_allowlist` is true and `global_allowed_files` is non-empty

Without a host handler, unknown file access fails closed.

## Shell Access Rules

Shell access is evaluated by command/prefix.

Important current details:

- explicit blocked commands win
- explicit allowed commands short-circuit approval
- session approvals are cached by base command
- persisted approvals store the base command
- manifest-declared shell prefixes are treated as allowed commands for that run
- strict command allowlist behavior only activates when `strict_command_allowlist` is true and `global_allowed_commands` is non-empty

The command may still run inside Docker, but shell execution is always treated as privileged.

## Network Access Rules

All sandbox HTTP(S) traffic flows through `internal/network.SandboxProxy`.

Current behavior:

- the proxy generates a CA certificate under `.falken/cache/proxy-ca.crt`
- the sandbox trusts that certificate on startup
- each request is checked against `CheckNetworkAccess(...)`
- project and session approvals can be saved either for an exact URL or for a domain
- denied requests receive HTTP 403 from the proxy

Without a host handler, unknown network access fails closed.

## Tools Versus Plugins

There is an intentional difference between tool permissions and plugin permissions.

### Tools

Wasm tools declare requested permissions in their manifests, but they can still trigger host prompts outside those declarations.

### Plugins

Plugin hook execution is treated as sandbox-only and stricter:

- plugin commands are checked against declared shell prefixes
- plugin file/network access can hard-fail when it exceeds the manifest
- plugin approval is persisted in `approved_plugins`

This is why plugin approval is surfaced separately in the startup flow.

## Background Processes

The background-process toolset runs inside the sandbox and stores logs under a sandbox temp area. Those processes are tracked by the host shell and are cleaned up on session shutdown.

## Diff Guardrails

Blocked files are protected twice:

1. before execution, by snapshot stubbing and access denial
2. after execution, by diff application guardrails

When applying a reviewed diff, blocked-file changes are filtered again before any copy-back happens.

## What This Protects Well

- accidental live edits to the real repository
- direct reading of common secrets from the sandbox view
- unapproved outbound HTTP(S) traffic
- permission self-escalation by editing `.falken.yaml`
- silent writes to protected runtime state files

## Practical Limits

This is strong process and policy isolation, but it is not a claim of perfect confinement. The model depends on:

- Docker behaving correctly
- permission checks being enforced in host code
- the proxy remaining in the network path
- humans reviewing patches before apply
