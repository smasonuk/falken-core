# Runtime State Layout

This document describes where Falken stores plans, memory, tasks, and subagent artifacts at runtime.

## Directory Structure

All session state lives under `.falken/state/current/` (relative to the workspace root).

```
.falken/
  state/
    current/
      history.jsonl          # Conversation history (Paths.HistoryPath)
      memory.json            # Structured agent memory (Paths.MemoryPath)
      plan.md                # Current implementation plan (Paths.PlanPath)
      tasks.json             # Task index (Paths.TasksPath)
      tasks/
        <taskID>/
          result.md          # Sub-agent output
          plan.md            # Copy of sub-agent's plan (if any)
          verify.md          # Verification agent output (if any)
      runs/
        <subrunID>/
          state/current/
            plan.md          # Sub-agent plan (SubRunPaths(...).PlanPath)
      truncations/           # Truncated content snapshots
      plugin_states/         # Per-plugin persisted state
  cache/                     # Transient runtime files (proxy cert, etc.)
  backups/                   # File backup snapshots
```

## Plan State

| Path | API | Description |
|------|-----|-------------|
| `state/current/plan.md` | `Paths.PlanPath()` | Main runner's implementation plan |
| `state/current/runs/<id>/state/current/plan.md` | `SubRunPaths(id).PlanPath()` | Sub-agent's plan during execution |
| `state/current/tasks/<taskID>/plan.md` | copied from sub-agent on completion | Persisted artifact of sub-agent plan |

## Task State

| Path | API | Description |
|------|-----|-------------|
| `state/current/tasks.json` | `Paths.TasksPath()` | Task index (all tasks) |
| `state/current/tasks/<taskID>/` | `Paths.TasksDir()/<taskID>/` | Per-task artifact directory |

## Memory

| Path | API | Description |
|------|-----|-------------|
| `state/current/memory.json` | `Paths.MemoryPath()` | Structured long-lived agent memory |
| `state/current/plan.md` | `Paths.PlanPath()` | Full Markdown implementation plan |
| `state/current/todos.json` | `Paths.TodosPath()` | Short execution checklist state |

## Sub-Agent Isolation

Sub-agents launched via `delegate_task` receive isolated state dirs via `SubRunPaths(runID)`:

- They share `WorkspaceDir` with the parent (same files).
- Their state (history, memory, plan, tasks) is under `state/current/runs/<runID>/`.
- Nesting is supported: a sub-agent can itself spawn sub-agents under deeper `runs/` paths.

## Invariants

Agents **must** use `write_plan` / `read_plan` tools for implementation plans.

Agents **must not**:
- Create `.agent_plan.md` or write plan files into the workspace.
- Use `write_file` to write plans.

These invariants are enforced by mode policy: `write_file` and `execute_command` are blocked in plan mode.
