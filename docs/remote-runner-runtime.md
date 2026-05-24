# Falken Remote-Runner Runtime Contract

This document describes Falken's no-local-agent-workspace runtime model. It is
limited to Falken process behavior and local development topology; Grace and
Kubernetes orchestration are out of scope here.

## Architecture

```text
client
  -> falken-agent-server
       owns LLM, session, conversation, planning, memory, command evidence
       uses a virtual /workspace path
       sends shell commands and managed file operations to runner
  -> falken-runner
       owns the real /workspace
       owns /runner-state for read tokens, backups, and managed file state
       serves /v1/execute and /v1/files/*
       receives proxy env vars for runner-executed commands
  -> falken-proxy (optional)
       enforces HTTP/HTTPS egress policy through the agent policy endpoint
```

Key invariant: in remote workspace mode the agent does not need a local
`/workspace` mount. All managed file operations and shell commands must go
through the runner.

EFS/RWX storage is not required for this Falken runtime model. The runner's
workspace can be a local directory, Kubernetes `emptyDir`, or an EBS RWO PVC.
If multiple pods must concurrently mount the same workspace, that is a
different storage requirement and this model is not a substitute for RWX.

## Storage

| Owner | Path | Purpose |
| --- | --- | --- |
| Agent | `/state` | Session, conversation, planning, memory, and command evidence state. Use a temp dir, PVC, or future external state backend. |
| Runner | `/workspace` | The real workspace. All file tools and shell commands see this storage through runner APIs. |
| Runner | `/runner-state` | Managed file service state: read-token registry, backups, and operation artifacts. This must be outside `/workspace`. |
| Proxy | none | The proxy does not need workspace or state mounts. |

Runner restart loses in-memory read-token state. After a restart, files must be
read again before edits, overwrites, or deletes that require a current read
token.

## Environment

Agent:

| Variable | Meaning |
| --- | --- |
| `FALKEN_AGENT_LISTEN` | Agent HTTP listen address. |
| `FALKEN_AGENT_TOKEN` | Bearer token for the agent API. |
| `FALKEN_WORKSPACE_DIR` | Virtual workspace path, usually `/workspace`; it need not exist in the agent container. |
| `FALKEN_STATE_DIR` | Agent-owned state root. |
| `FALKEN_LLM_PROVIDER` | LLM provider selection. |
| `FALKEN_LLM_API_KEY` | LLM API key. |
| `FALKEN_LLM_MODEL` | LLM model name. |
| `FALKEN_LLM_BASE_URL` | Optional LLM endpoint override. |
| `FALKEN_RUNNER_ENDPOINT` | Runner base URL. |
| `FALKEN_RUNNER_COMMAND_TOKEN` | Token used for `/v1/execute`. Falls back to `FALKEN_RUNNER_TOKEN` where supported. |
| `FALKEN_RUNNER_WORKSPACE_TOKEN` | Token used for `/v1/files/*`. Falls back to `FALKEN_RUNNER_TOKEN` where supported. |
| `FALKEN_NETWORK_POLICY_LISTEN` | Agent-side network policy endpoint listen address. |
| `FALKEN_NETWORK_POLICY_SIDECAR_ENDPOINT` | Endpoint the proxy sidecar uses to reach policy. |
| `FALKEN_NETWORK_POLICY_TOKEN` | Bearer token for policy checks. |
| `FALKEN_NETWORK_ALLOWLIST` | Network allowlist configuration. |
| `FALKEN_PLAN_ROUTING` | Plan routing mode. |

Runner:

| Variable | Meaning |
| --- | --- |
| `FALKEN_RUNNER_LISTEN` | Runner HTTP listen address. |
| `FALKEN_RUNNER_WORKSPACE` | Real runner workspace root, usually `/workspace`. |
| `FALKEN_RUNNER_FILE_STATE_DIR` | Runner file-operation state root, usually `/runner-state`; must be outside workspace. |
| `FALKEN_RUNNER_COMMAND_TOKEN` | Token accepted by `/v1/execute`. |
| `FALKEN_RUNNER_WORKSPACE_TOKEN` | Token accepted by `/v1/files/*`. |
| `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY` | Proxy env injected into runner-executed commands. |
| `http_proxy`, `https_proxy`, `no_proxy` | Lowercase proxy env variants. |

Proxy:

| Variable | Meaning |
| --- | --- |
| `FALKEN_PROXY_LISTEN` | Proxy listen address. |
| `FALKEN_PROXY_POLICY_SOCKET` | Agent policy endpoint. |
| `FALKEN_PROXY_POLICY_TOKEN` | Bearer token for policy endpoint requests. |
| `FALKEN_PROXY_LOG_JSON` | Emit JSON proxy logs. |

Command and workspace tokens can be the same for local development, but separate
tokens are preferred: the command token authorizes shell execution and the
workspace token authorizes managed file operations.

## Local Three-Process Example

```bash
# terminal 1: optional proxy
FALKEN_PROXY_LISTEN=:8081 \
FALKEN_PROXY_POLICY_SOCKET=tcp://127.0.0.1:9091 \
FALKEN_PROXY_POLICY_TOKEN=policy-token \
falken-proxy

# terminal 2: runner
mkdir -p /tmp/falken-runner-workspace /tmp/falken-runner-state
FALKEN_RUNNER_LISTEN=:8082 \
FALKEN_RUNNER_WORKSPACE=/tmp/falken-runner-workspace \
FALKEN_RUNNER_FILE_STATE_DIR=/tmp/falken-runner-state \
FALKEN_RUNNER_COMMAND_TOKEN=command-token \
FALKEN_RUNNER_WORKSPACE_TOKEN=workspace-token \
HTTP_PROXY=http://127.0.0.1:8081 \
HTTPS_PROXY=http://127.0.0.1:8081 \
NO_PROXY=localhost,127.0.0.1,::1 \
falken-runner

# terminal 3: agent
mkdir -p /tmp/falken-agent-state
FALKEN_AGENT_LISTEN=:8080 \
FALKEN_AGENT_TOKEN=agent-token \
FALKEN_WORKSPACE_DIR=/workspace \
FALKEN_STATE_DIR=/tmp/falken-agent-state \
FALKEN_RUNNER_ENDPOINT=http://127.0.0.1:8082 \
FALKEN_RUNNER_COMMAND_TOKEN=command-token \
FALKEN_RUNNER_WORKSPACE_TOKEN=workspace-token \
FALKEN_PLAN_ROUTING=disabled \
falken-agent-server
```

In this setup `/workspace` is virtual to the agent. Files written by `write_file`
and commands run by `execute_command` operate in
`/tmp/falken-runner-workspace`.
