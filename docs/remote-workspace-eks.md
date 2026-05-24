# EKS Remote Workspace Deployment Contract

This document is the Falken-side contract for any  Kubernetes
manifest builder. It covers the agent, runner, and proxy pods needed for the
remote-workspace runtime model.

## Invariant

The agent pod must not mount `/workspace`. The runner pod is the only pod that
owns the real workspace. Shell commands and managed file operations flow through
the runner APIs.

Runner-local workspace operations are intentionally created with
`LocalPolicyAllowAll` because the runner API is protected by bearer tokens and
network policy. Falken policy enforcement happens in the agent before requests
are forwarded, through `policyCheckingWorkspaceOperations`.

## Agent Pod

Required:

```text
FALKEN_AGENT_LISTEN=:8080
FALKEN_AGENT_TOKEN=<agent-api-token>
FALKEN_WORKSPACE_DIR=/workspace
FALKEN_STATE_DIR=/state
FALKEN_LLM_PROVIDER=<provider>
FALKEN_LLM_API_KEY=<secret>
FALKEN_LLM_MODEL=<model>
FALKEN_RUNNER_ENDPOINT=http://<runner-service>:8080
FALKEN_RUNNER_COMMAND_TOKEN=<command-token>
FALKEN_RUNNER_WORKSPACE_TOKEN=<workspace-token>
FALKEN_REQUIRE_SPLIT_RUNNER_TOKENS=true
FALKEN_ALLOW_RUNNER_RESTART=false
FALKEN_REMOTE_WORKSPACE_SMOKE_CHECK=true
```

Mounts:

```text
/state -> emptyDir, PVC, or future external state backend
```

Do not mount:

```text
/workspace
/runner-state
```

Optional network-policy/proxy env:

```text
FALKEN_NETWORK_POLICY_LISTEN=:9091
FALKEN_NETWORK_POLICY_SIDECAR_ENDPOINT=tcp://<agent-service>:9091
FALKEN_NETWORK_POLICY_TOKEN=<policy-token>
FALKEN_NETWORK_ALLOWLIST=<comma-separated allowlist>
FALKEN_RUNNER_PREFLIGHT_TIMEOUT_SECONDS=2
FALKEN_RUNNER_WORKSPACE_REQUEST_TIMEOUT_SECONDS=600
FALKEN_RUNNER_COMMAND_REQUEST_TIMEOUT_SECONDS=0
FALKEN_BASE_SYSTEM_PROMPT=<optional override>
```

Operational checks:

```bash
falken-agent-server --check-config
```

The agent exposes `GET /v1/status` with bearer auth. It reports whether the
session is using remote workspace operations, runner readiness, runner instance
ID, runner endpoint, network modes, split-token enforcement, state backend, and
whether direct workspace-capable provider tools are allowed. It includes no
secrets.

`GET /readyz` fails until the Falken session is started and, in remote-runner
mode, the runner passes preflight. Preflight calls runner `/readyz`, runner
`/v1/status`, and a non-mutating authenticated `/v1/files/glob` smoke check so a
wrong workspace token is caught before a user session is exposed.

## Runner Pod

Required:

```text
FALKEN_RUNNER_LISTEN=:8080
FALKEN_RUNNER_WORKSPACE=/workspace
FALKEN_RUNNER_FILE_STATE_DIR=/runner-state
FALKEN_RUNNER_COMMAND_TOKEN=<command-token>
FALKEN_RUNNER_WORKSPACE_TOKEN=<workspace-token>
FALKEN_REQUIRE_SPLIT_RUNNER_TOKENS=true
FALKEN_RUNNER_SHELL=/bin/sh
FALKEN_RUNNER_MAX_WORKSPACE_REQUEST_BYTES=10485760
```

Mounts:

```text
/workspace    -> EBS ReadWriteOnce PVC, emptyDir, or another runner-owned workspace volume
/runner-state -> emptyDir or small PVC; must not be inside /workspace
```

Proxy env for runner-executed commands:

```text
HTTP_PROXY=http://<proxy-service>:8080
HTTPS_PROXY=http://<proxy-service>:8080
NO_PROXY=localhost,127.0.0.1,::1
http_proxy=http://<proxy-service>:8080
https_proxy=http://<proxy-service>:8080
no_proxy=localhost,127.0.0.1,::1
```

Operational checks:

```bash
falken-runner --check-config
GET /readyz
GET /v1/status
```

The runner generates a `runner_instance_id` at startup and returns it in
`X-Falken-Runner-Instance`. Remote clients fail closed if this ID changes during
a session unless `FALKEN_ALLOW_RUNNER_RESTART=true` is configured. A changed ID
usually means `/runner-state` may have lost read-token state; recreate the
session.

Command execution and managed file operations share the same instance guard on
the agent side. If either path observes a different runner instance, the session
fails closed by default.

`FALKEN_RUNNER_MAX_WORKSPACE_REQUEST_BYTES` is optional. When unset, the remote
workspace API uses a 10 MiB request body limit for file-operation requests such
as `write_file` and `apply_patch`.

## Proxy Pod

Required when proxy-only egress is enabled:

```text
FALKEN_PROXY_LISTEN=:8080
FALKEN_PROXY_POLICY_SOCKET=tcp://<agent-service>:9091
FALKEN_PROXY_POLICY_TOKEN=<policy-token>
FALKEN_PROXY_LOG_JSON=true
```

Mounts:

```text
none
```

The proxy should not receive workspace, state, LLM, AWS, or Kubernetes
credentials.

## Network Policy

Minimum intended flow:

```text
server -> agent:8080
agent -> runner:8080
runner -> proxy:8080
runner -> DNS
proxy -> agent:9091
proxy -> DNS
proxy -> internet
agent -> LLM/model endpoint
```

The runner workspace API is high value. Kubernetes NetworkPolicy must prevent
non-agent pods from reaching the runner service.

Managed file operations are mediated by the agent-side file policy wrapper
before reaching the runner. Shell commands are still a separate mutation path:
commands run in the runner workspace and can create, edit, or delete files using
normal shell tools. Command policy and review controls remain the enforcement
layer for that path.

## Storage Notes

EFS/RWX is not required for this model. The runner can use an EBS RWO PVC
because only the runner mounts `/workspace`.

`emptyDir` for `/workspace` loses the workspace on pod death. EBS RWO can provide
per-session persistence. `/runner-state` may use `emptyDir`, but runner restart
then loses read-token state and sessions should be recreated unless the operator
explicitly allows runner restarts.
