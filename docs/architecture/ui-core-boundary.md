# UI/Core Boundary

## Purpose

This document records the intended boundary between `falken-core` and the sibling `falken-term` host application.

## Ownership

### Core public API

- `pkg/falken`
- `pkg/pluginsdk`
- `pkg/workspacepath`

### Core implementation

- `internal/agent`
- `internal/bash`
- `internal/extensions`
- `internal/host`
- `internal/network`
- `internal/permissions`
- `internal/runtimeapi`
- `internal/runtimectx`
- `internal/tasks`
- `internal/todo`


## Allowed Dependencies

- terminal app -> `pkg/falken`
- terminal app -> its own local packages
- `pkg/falken` -> core internals
- tests anywhere -> internal packages as needed

## Forbidden Dependencies

- core internals -> terminal app
- terminal app -> `internal/*` packages from `falken-core`
- hosts depending directly on `internal/runtimeapi` when the equivalent type is re-exported through `pkg/falken`

## Boundary Rules

- Host-facing lifecycle must be expressed through `pkg/falken.Session`.
- Host-facing events must be expressed through the `pkg/falken` event types.
- Host-facing approval callbacks must be expressed through `pkg/falken.InteractionHandler`.
- The host may inspect `ToolInfos()` and `PluginInfos()`, but it should not know about `extensions.WasmTool` or `extensions.WasmHook` directly.
- The host owns routing, modal UX, startup checks, and diff review.
- Core owns sandboxing, permissions, tool execution, history, memory, and delegated-task behavior.

## Notes On Current State

- The terminal app currently uses only the public `pkg/falken` API.
- Plugin approval is a host concern, but plugin discovery metadata is provided by `Session.PluginInfos()`.
- Plan mode is exposed to the TUI through `Session.ForcePlanMode(...)` plus plan-approval callbacks.
- Runtime-internal event payloads are translated into public `pkg/falken.Event` values before reaching the host.

## Non-goals

These are intentionally out of scope for the current boundary:

- moving runtime internals into a second repository
- exposing `internal/...` types directly to hosts
- changing the `.falken.yaml` format just to satisfy UI needs
- letting the TUI bypass the session and talk directly to sandbox or agent internals
