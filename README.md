# Falken Core

`falken-core` is the reusable Go runtime behind Falken. It provides the embeddable session API, the agent loop, the Docker-backed sandbox, the permission system, and the Wasm extension loader used by the sibling `falken-term` TUI.

## What lives here

- `pkg/falken`
  Public embedding API. Hosts use this package to create sessions, start the sandbox, run prompts, inspect tools/plugins, and apply reviewed diffs.
- `examples/headless`
  Minimal non-TUI embedding example built on the public API.
- `internal/agent`
  Streaming model loop, history compaction, memory, todos, delegated tasks, verification runs, and built-in host tools.
- `internal/host`
  Workspace snapshotting, Docker sandbox lifecycle, shell execution, diff generation/application, background processes, and Wasm host functions.
- `internal/permissions`
  File, shell, and network policy enforcement plus `.falken.yaml` loading/saving.
- `internal/network`
  MITM proxy for sandbox HTTP(S) traffic.
- `internal/extensions`
  Embedded/external Wasm tool and plugin loading through Wazero.
- `extensions/`
  Canonical source for embedded tools and plugins.

## Runtime model

At a high level, Falken works like this:

1. A host creates a `falken.Session`.
2. The session resolves the workspace and `.falken` state directory, loads tools/plugins, and constructs the runner.
3. `Session.Start(...)` creates a workspace snapshot, starts the proxy, and boots the Docker sandbox.
4. `Session.Run(...)` streams model output, executes built-in and Wasm tools, and emits typed runtime events.
5. The host generates a diff from sandbox changes and decides whether to apply it back to the real workspace.

The agent does not work directly in the real repository. It works in a temporary snapshot mounted into the sandbox container.

## State and config

- `.falken/`
  Runtime state such as chat history, memory, delegated tasks, todos, caches, backups, and plugin state.
- `.falken.yaml`
  Project-level config for sandbox image selection, cache mounts, permission rules, and approved plugins.

See [docs/embedding-and-state.md](./docs/embedding-and-state.md) and [docs/security-and-permissions.md](./docs/security-and-permissions.md) for details.

## Build and run

- Build the runtime packages, embedded Wasm assets, and headless example:
  ```sh
  make all
  ```

- Build just the core packages and headless example:
  ```sh
  ./build.sh
  ```

- Build the sandbox image expected by the default config:
  ```sh
  ./build_docker.sh
  ```

- Run the headless example:
  ```sh
  ./run.sh
  ```

## Docs

Start with [docs/README.md](./docs/README.md). The most useful follow-up docs are:

- [docs/architecture.md](./docs/architecture.md)
- [docs/runtime-flow.md](./docs/runtime-flow.md)
- [docs/security-and-permissions.md](./docs/security-and-permissions.md)
- [docs/extensions-and-tools.md](./docs/extensions-and-tools.md)
- [docs/embedding-and-state.md](./docs/embedding-and-state.md)
