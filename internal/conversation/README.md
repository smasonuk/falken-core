# Conversation State

`internal/conversation` owns conversation-scoped runtime state and validation helpers.

The package is responsible for:

- Implementation plans and plan validation.
- Todo normalization, validation, rendering, and storage management.
- Memory normalization, validation, rendering, and storage management.
- Command evidence records, completion checks, and command-evidence storage management.
- The `ConversationState` facade that coordinates those state managers.

This package should remain independent of `internal/agent`. It is the lower-level state layer used by the agent runtime and built-in tools, not an agent orchestration package.

Dependency direction should stay:

```text
internal/agent -> internal/conversation
built-in tools -> internal/conversation
internal/conversation -> store/state/domain dependencies
```

Keeping `conversation` free of `agent` dependencies makes state management reusable by tools and session code without pulling in the full runner, routing, LLM, or tool-execution surface.
