# Falken Core Docs

This folder documents the current `falken-core` runtime as it exists in the code today.

## What Falken Core is

Falken Core is a Go runtime for code-oriented agent execution. It combines:

- an embeddable public API in `pkg/falken`
- a Docker-backed sandbox and permission system
- a streaming agent loop with built-in host tools
- a Wasm extension system for tools and plugins
- typed events that let hosts such as `falken-term` render live execution

The runtime is host-mediated by design. A host owns the real workspace, receives permission requests, and decides whether sandbox changes are applied back to disk.

## Read This First

If you are new to the codebase, read the docs in this order:

1. [Architecture](./architecture.md)
2. [Runtime Flow](./runtime-flow.md)
3. [Security And Permissions](./security-and-permissions.md)
4. [Extensions And Tools](./extensions-and-tools.md)
5. [Embedding And State](./embedding-and-state.md)

## Document Map

- [Architecture](./architecture.md)
  Package layout, layering, and the responsibilities of the host, runtime, and sandbox code.

- [Runtime Flow](./runtime-flow.md)
  Session construction, startup routes, run loop behavior, diff review, and shutdown.

- [Security And Permissions](./security-and-permissions.md)
  Snapshotting, Docker isolation, permission checks, blocked files, strict allowlists, and review guardrails.

- [Extensions And Tools](./extensions-and-tools.md)
  Built-in host tools, Wasm tool loading, dynamic activation, embedded extension assets, and plugin approval behavior.

- [Embedding And State](./embedding-and-state.md)
  Public API surface, session lifecycle, event model, and `.falken/` storage layout.

- [UI/Core Boundary](./architecture/ui-core-boundary.md)
  The boundary between `falken-core` and the sibling `falken-term` host application.
