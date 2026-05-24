# Runtime Provider Split Migration

`falken.ExecutionConfig.SandboxBackend` has been replaced by `RuntimeProfile`.
Core treats this value as provider-owned and no longer exports Docker backend
constants.

The default Docker profiles are exported by `falken-extra/runtime/defaults`:

- `defaults.RuntimeProfileDockerCLI`
- `defaults.RuntimeProfileDockerEngine`

Existing CLI users can continue using `--sandbox-backend docker_cli|docker_engine`.
