# Deferred Capability Seams

Falken Core v1 intentionally ships without a general runtime verification gate, delegated jobs, delegated merge-back, Verify mode, Explore mode, or non-Wasm extension runtimes. It does include a lightweight implementation-submission tool for active plans/todos. This note records where larger features would plug in later without implying they are currently implemented.

## Current V1 Boundaries

- The public API exposes `default` and `plan` modes only.
- Normal completion happens when the agent loop finishes without more tool calls and no active implementation plan/todos require submission.
- `submit_plan_implementation` blocks final completion only for active plans/todos. It checks completed todos and recent command evidence, then clears the active plan/todos on acceptance.
- There is no general submit state machine, hard verification gate, or test runner gate for all runs.
- Core does not discover or execute Wasm tools directly; Wasm-backed extension adapters live outside core and register through `ToolProviders`.
- Session owns runtime resources and enforces lifecycle/run guards.

## Future Verification

Stronger verification can be added around the existing agent completion boundary:

- The agent runner already produces structured run results and `run_completed`/`run_failed` events.
- Command evidence already records compact command outcomes and policy decisions for completion review.
- A future verifier should sit between core loop completion and final `run_completed` emission.
- Verification state should remain separate from the public v1 completion callback and should not reuse `OnCompleted` as a gate.

Until such a verifier exists, v1 has no general test/verification gate beyond the active plan/todo submission workflow.

## Future Jobs and Delegation

Durable jobs and delegated sub-runs should plug in beside, not inside, the top-level session run guard:

- Session currently supports at most one active top-level run.
- A future job manager can own durable job records under the state layout.
- Delegated sub-runs can reuse the agent runner, stores, tool execution surface, and policy context with explicit child-run identity.
- Merge-back should be a managed file operation flow that goes through read tokens, stale-read validation, backups, secret scanning, and policy checks.

No delegated job APIs are public in v1.

## Future Modes

Additional modes should extend the existing mode-policy seam:

- Add new mode constants and validation in the agent mode layer.
- Define mode-specific tool filtering in one place.
- Include mode context in prepared system prompts without duplicating sections.
- Keep enforcement in code, not prompt text.

Verify and Explore modes are not exposed by v1. If added later, they should use explicit mode policy and command evidence rather than inferred command field names.

## Future Extension Runtimes

The extension platform records runtime kind in manifests and metadata, but v1 validates Wasm as the only supported runtime. A future runtime adapter should:

- Add an explicit manifest runtime kind.
- Implement a separate executor that preserves identity and declared permission propagation.
- Keep discovery, registration, activation, and execution as distinct lifecycle steps.
- Preserve session-owned cleanup.

Non-Wasm runtimes are not partially available in v1.
