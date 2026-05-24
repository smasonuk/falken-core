# Built-in Tools

`internal/builtintools` is the registry and compatibility package for Falken's built-in tools.

The root package owns:

- Tool registration order through `registry.go`.
- Lookup helpers such as `All`, `ByName`, and `IsBuiltin`.
- Temporary compatibility aliases that preserve existing root-package names while implementations live in domain packages.
- Contract tests that protect built-in tool descriptors and ordering.

Tool implementations live in domain subpackages:

- `filetools` for workspace file read, write, edit, delete, patch, glob, and grep tools.
- `commandtools` for shell command execution tooling.
- `planningtools` for plan, todo, command-evidence, and implementation-submission tools.
- `memorytools` for conversation memory tools.

Shared tool API types and helper functions live in `internal/builtintools/api`. Domain subpackages should import `builtintools/api` for `Tool`, `Descriptor`, `Host`, schemas, results, and argument decoding.

Domain subpackages must not import the root `internal/builtintools` package. Dependency direction should stay:

```text
builtintools root -> domain tool packages -> builtintools/api
```

This keeps the registry from becoming a shared implementation dependency and avoids import cycles as tools continue moving into focused packages.
