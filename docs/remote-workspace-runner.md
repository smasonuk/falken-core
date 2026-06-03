# Remote Workspace Runner 

Lets the agent pod run without a shared `/workspace` mount. The
agent keeps LLM/session/conversation/planning/memory/command-evidence state,
while a runner process owns the real workspace and serves both command execution
and managed file operations.

## Architecture

```text
Agent pod
  - LLM credentials and session runtime
  - conversation, plan, todo, memory, command evidence state
  - command truncation artifacts under agent state
  - shell and network policy evaluation
  - no local workspace snapshot

      HTTP bearer token(s)
      /v1/execute
      /v1/files/*

Runner pod
  - owns /workspace
  - executes shell commands in that workspace
  - serves managed read/glob/grep/write/edit/patch/delete
  - owns in-memory read-before-write token registry
  - has no LLM, AWS, or Kubernetes credentials
```

The agent uses `remoteexec.Runtime` for shell commands and
`remoteworkspace.Client` for file operations. Core uses a virtual execution
state for this profile: working directories are constrained lexically under the
virtual workspace root on the agent, then the runner performs final filesystem
validation against its real workspace. Provider `CheckFileAccess` calls use the
same virtual path resolver, so checking access to existing workspace files does
not require the agent pod to stat `/workspace`.
Custom provider tools, including Wasm-backed adapters registered through a
provider, that declare workspace read or mutation capabilities are rejected in
remote workspace mode by default; they either need to use managed workspace
operations or be explicitly allowed by the host.

## State Ownership

The runner owns `/workspace` and the managed file service token registry.
Read-before-write tokens are in memory inside the runner process. A runner
restart loses those tokens, so existing files must be reread before later
`write_file`, `edit_file`, `multi_edit`, `apply_patch`, or `delete_file` calls.

The agent owns conversation state, planning state, memory, todos, command
evidence, and command truncation artifacts. Command output artifacts are not
workspace files in remote workspace mode, so workspace `grep` is not responsible
for reading them.

The agent never copies full workspace snapshots locally.

## Storage Notes

`emptyDir` can be used for short-lived runner workspaces, but the workspace is
lost when the pod dies. EBS RWO can provide per-session persistence without
requiring RWX/EFS. Design B-lite is not a substitute for RWX storage when
multiple pods must concurrently mount and mutate the same workspace.

## Environment

| Variable | Owner | Purpose |
| --- | --- | --- |
| `FALKEN_RUNNER_ENDPOINT` | agent | Base URL for runner command and file APIs. |
| `FALKEN_RUNNER_TOKEN` | agent and runner | Backward-compatible bearer token fallback for `/v1/execute` and `/v1/files/*`. |
| `FALKEN_RUNNER_COMMAND_TOKEN` | agent and runner | Optional token for `/v1/execute`; falls back to `FALKEN_RUNNER_TOKEN`. |
| `FALKEN_RUNNER_WORKSPACE_TOKEN` | agent and runner | Optional token for `/v1/files/*`; falls back to `FALKEN_RUNNER_TOKEN`. |
| `FALKEN_RUNNER_WORKSPACE` | runner | Real workspace root owned by the runner. Defaults to `/workspace`; the agent-facing protocol root remains `/workspace`. |
| `FALKEN_RUNNER_FILE_STATE_DIR` | runner | Managed file service state and backups. Defaults to `/runner-state`; must not resolve inside `FALKEN_RUNNER_WORKSPACE`. |
| `FALKEN_STATE_BACKEND` | agent | Optional agent state backend. Empty or `file` uses state files under `/state`; `memory` uses ephemeral in-memory state. |
| `FALKEN_ALLOW_WORKSPACE_TOOLS_IN_REMOTE_MODE` | agent | Optional escape hatch for custom workspace-capable tools in remote workspace mode. Defaults to false. |
| `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY` and lowercase variants | runner | Proxy environment injected into runner-executed commands. |
| `FALKEN_NETWORK_POLICY_LISTEN` | agent | Policy endpoint listen address for proxy-only egress. |
| `FALKEN_NETWORK_POLICY_SIDECAR_ENDPOINT` | agent and proxy sidecar | Reachable TCP endpoint for network policy checks. |
| `FALKEN_NETWORK_POLICY_TOKEN` | agent and proxy sidecar | Bearer token for network policy checks. |

## Kubernetes Layout

Design B-lite uses separate Deployments for the agent, runner, and proxy.

```text
agent Deployment
  env: FALKEN_RUNNER_ENDPOINT, FALKEN_RUNNER_TOKEN or split tokens
  env: FALKEN_WORKSPACE_DIR=/workspace, FALKEN_STATE_DIR=/state
  mounts: /state only
  secrets: LLM/API credentials

runner Deployment
  env: FALKEN_RUNNER_WORKSPACE=/workspace
  env: FALKEN_RUNNER_FILE_STATE_DIR=/runner-state
  env: HTTP_PROXY, HTTPS_PROXY, NO_PROXY
  mounts: /workspace from EBS RWO PVC or emptyDir
  mounts: /runner-state from emptyDir or a small PVC
  secrets: runner command/workspace tokens only

proxy Deployment
  env: FALKEN_PROXY_POLICY_SOCKET=tcp://<agent-service>:9091
  mounts: none for workspace or state
```

The agent must not mount the runner workspace or `/runner-state`. The runner
must not receive LLM credentials. The proxy must not receive workspace or agent
state volumes.

NetworkPolicy should allow only:

```text
Grace server -> agent:8080
agent -> runner:8080
runner -> proxy:8080
runner -> DNS
proxy -> agent:9091
proxy -> DNS
proxy -> internet
agent -> LLM/model endpoint
```

The runner workspace PVC should use the existing EBS `ReadWriteOnce`
StorageClass. No EFS/RWX volume is required for one runner owning one workspace.

## Security Assumptions

The runner token is secret. Runner endpoints are internal and authenticated with
that token. The runner should not receive LLM credentials, AWS credentials, or a
Kubernetes service account token. Runner network egress should be forced through
the configured proxy when strict network policy is required. Proxy environment
for shell commands is configured on the runner process/server; the agent-side
`remoteexec.Runtime` does not push proxy settings to `/v1/execute`.

All file operations go through Core's managed file service. The runner must not
expose endpoints that return full workspace snapshots.

## Troubleshooting

Missing runner token: `falken-runner` refuses to start unless
`FALKEN_RUNNER_TOKEN` or both split tokens are configured. The agent
`--check-config` path requires `FALKEN_RUNNER_ENDPOINT` and either
`FALKEN_RUNNER_TOKEN` or both `FALKEN_RUNNER_COMMAND_TOKEN` and
`FALKEN_RUNNER_WORKSPACE_TOKEN` for the Kubernetes remote-runner profile.

Unauthorized file endpoint: verify the agent and runner use the same
workspace token. `/v1/files/*` returns structured JSON with HTTP 401 for
missing or wrong bearer tokens.

Missing or stale read token: reread the target file through `read_file` or
`read_files`. This is also required after runner restart because the token
registry is intentionally in memory.

Workspace lost with `emptyDir`: the runner pod died or restarted with ephemeral
workspace storage. Use a per-session persistent volume such as EBS RWO when the
workspace must survive pod death.

Agent accidentally requires local `/workspace`: confirm the Kubernetes
remote-runner profile is selected and the runtime provider returns both
`remoteexec.Runtime` and `remoteworkspace.Client`. In this mode Core should use
virtual execution state and should not instantiate local managed file
operations.

Custom tool fails in remote mode: provider metadata declares workspace
read/mutation. Convert the tool to managed workspace operations, run it only in
local/shared-storage mode, or deliberately set
`FALKEN_ALLOW_WORKSPACE_TOOLS_IN_REMOTE_MODE=true` after reviewing the risk.
