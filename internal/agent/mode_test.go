package agent_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/smasonuk/falken-core/internal/agent"
	"github.com/smasonuk/falken-core/internal/extensions/manifest"
	"github.com/smasonuk/falken-core/internal/extensions/tools"
	"github.com/smasonuk/falken-core/internal/policy"
	"github.com/smasonuk/falken-core/internal/store"
)

func TestModeStateEnterExitAndReset(t *testing.T) {
	layout := testAgentLayout(t)
	mode := agent.NewModeState(store.NewPlanStore(layout))

	if got := mode.Current(); got != agent.ModeDefault {
		t.Fatalf("initial mode = %q, want default", got)
	}
	if err := mode.EnterPlan(); err != nil {
		t.Fatalf("EnterPlan: %v", err)
	}
	if got := mode.Current(); got != agent.ModePlan {
		t.Fatalf("mode after enter = %q, want plan", got)
	}
	if err := mode.EnterPlan(); err != nil {
		t.Fatalf("EnterPlan again: %v", err)
	}

	if err := mode.Plan().Write(validImplementationPlanForTest()); err != nil {
		t.Fatalf("Write plan: %v", err)
	}
	if err := mode.ExitPlan(); err != nil {
		t.Fatalf("ExitPlan: %v", err)
	}
	if got := mode.Current(); got != agent.ModeDefault {
		t.Fatalf("mode after exit = %q, want default", got)
	}
	if err := mode.ExitPlan(); err != nil {
		t.Fatalf("ExitPlan again should be deterministic no-op: %v", err)
	}

	if err := mode.EnterPlan(); err != nil {
		t.Fatalf("EnterPlan before reset: %v", err)
	}
	mode.Reset()
	if got := mode.Current(); got != agent.ModeDefault {
		t.Fatalf("mode after reset = %q, want default", got)
	}
}

func TestPlanStoreIntegrationAndExitValidation(t *testing.T) {
	layout := testAgentLayout(t)
	mode := agent.NewModeState(store.NewPlanStore(layout))

	if err := mode.EnterPlan(); err != nil {
		t.Fatalf("EnterPlan: %v", err)
	}
	plan, err := mode.Plan().Read()
	if err != nil {
		t.Fatalf("Read initialized plan: %v", err)
	}
	if strings.TrimSpace(plan) == "" {
		t.Fatal("entering plan mode should initialize starter plan content")
	}
	if err := mode.ExitPlan(); !errors.Is(err, agent.ErrInvalidPlan) {
		t.Fatalf("ExitPlan with starter plan error = %v, want ErrInvalidPlan", err)
	}
	if got := mode.Current(); got != agent.ModePlan {
		t.Fatalf("mode after failed exit = %q, want still plan", got)
	}

	want := validImplementationPlanForTest()
	if err := mode.Plan().Write(want); err != nil {
		t.Fatalf("Write meaningful plan: %v", err)
	}
	got, err := mode.Plan().Read()
	if err != nil {
		t.Fatalf("Read meaningful plan: %v", err)
	}
	if got != want {
		t.Fatalf("plan = %q, want %q", got, want)
	}
	if err := mode.ExitPlan(); err != nil {
		t.Fatalf("ExitPlan with meaningful plan: %v", err)
	}
}

func TestValidateImplementationPlanRejectsMissingPlaceholderAndShortPlans(t *testing.T) {
	tests := []struct {
		name string
		plan string
		want string
	}{
		{
			name: "missing heading",
			plan: "# Goal\nImplement a complete feature with enough content to pass the length check.\n\n# Files\n- internal/agent/mode.go\n\n# Verification\nRun go test ./internal/agent.",
			want: "Plan is missing required heading: Changes",
		},
		{
			name: "placeholder section",
			plan: "# Goal\nImplement a complete feature with enough content to pass the length check.\n\n# Files\nTBD\n\n# Changes\n1. Update the runtime behavior carefully.\n\n# Verification\nRun go test ./internal/agent.",
			want: "placeholder-only",
		},
		{
			name: "too short",
			plan: "# Goal\nOk\n# Files\nOk\n# Changes\nOk\n# Verification\nOk",
			want: "too short",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := agent.ValidateImplementationPlan(tt.plan)
			if !errors.Is(err, agent.ErrInvalidPlan) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ValidateImplementationPlan error = %v, want ErrInvalidPlan containing %q", err, tt.want)
			}
		})
	}
	if err := agent.ValidateImplementationPlan(validImplementationPlanForTest()); err != nil {
		t.Fatalf("ValidateImplementationPlan(valid): %v", err)
	}
}

func validImplementationPlanForTest() string {
	return "# Goal\nImplement the requested runtime mode behavior with enough detail to guide the change.\n\n# Files\n- internal/agent/mode.go\n- internal/agent/mode_test.go\n\n# Changes\n1. Update the mode behavior in the runtime.\n2. Keep tests aligned with the public contract.\n\n# Verification\nRun go test ./internal/agent to confirm mode behavior."
}

func TestModeToolFiltering(t *testing.T) {
	available := []tools.Entry{
		{Name: "write_file", Description: "write", Category: "files", Runtime: "builtin"},
		{Name: "read_file", Description: "read", Category: "files", Runtime: "builtin", Safety: tools.Safety{PlanSafe: true}},
		{Name: "glob", Description: "glob", Category: "files", Runtime: "builtin", Safety: tools.Safety{PlanSafe: true, ReadsWorkspace: true}},
		{Name: "grep", Description: "grep", Category: "files", Runtime: "builtin", Safety: tools.Safety{PlanSafe: true, ReadsWorkspace: true}},
		{Name: "shell_execute", Description: "shell", Category: "command", Runtime: "builtin"},
		{Name: "read_plan", Description: "plan", Category: "planning", Runtime: "builtin", Safety: tools.Safety{PlanSafe: true}},
		{Name: "write_plan", Description: "plan", Category: "planning", Runtime: "builtin", Safety: tools.Safety{PlanSafe: true, UsesHostState: true, ReadsHostState: true, MutatesHostState: true}},
		{Name: "write_todos", Description: "todos", Category: "planning", Runtime: "builtin", Safety: tools.Safety{PlanSafe: true, UsesHostState: true, ReadsHostState: true, MutatesHostState: true}},
		{Name: "read_command_evidence", Description: "command evidence", Category: "planning", Runtime: "builtin", Safety: tools.Safety{PlanSafe: true, UsesHostState: true, ReadsHostState: true}},
		{Name: "submit_plan_implementation", Description: "submit", Category: "planning", Runtime: "builtin", Safety: tools.Safety{PlanSafe: true, UsesHostState: true, ReadsHostState: true}},
		{Name: "read_memory", Description: "memory", Category: "memory", Runtime: "builtin", Safety: tools.Safety{PlanSafe: true, UsesHostState: true, ReadsHostState: true}},
		{Name: "update_memory", Description: "memory", Category: "memory", Runtime: "builtin", Safety: tools.Safety{PlanSafe: true, UsesHostState: true, ReadsHostState: true, MutatesHostState: true}},
		{Name: "provider_planner", Description: "provider planning metadata", Category: "planning", PackageName: "provider", Safety: tools.Safety{PlanSafe: true}},
		{Name: "custom_inspect", Description: "inspect", Keywords: []string{"inspect"}, Runtime: manifest.RuntimeWasm, Safety: tools.Safety{ReadsWorkspace: true}},
		{Name: "mystery", Description: "unknown"},
	}

	defaultTools, err := agent.FilterTools(agent.ModeDefault, available)
	if err != nil {
		t.Fatalf("FilterTools default: %v", err)
	}
	if got, want := toolNames(defaultTools), []string{"write_file", "read_file", "glob", "grep", "shell_execute", "read_plan", "write_todos", "read_command_evidence", "submit_plan_implementation", "read_memory", "update_memory", "provider_planner", "custom_inspect", "mystery"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("default tools = %v, want %v", got, want)
	}

	planTools, err := agent.FilterTools(agent.ModePlan, available)
	if err != nil {
		t.Fatalf("FilterTools plan: %v", err)
	}
	got := toolNames(planTools)
	want := []string{"read_file", "glob", "grep", "read_plan", "write_plan", "write_todos", "read_command_evidence", "submit_plan_implementation", "read_memory", "update_memory", "provider_planner", "custom_inspect"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("plan tools = %v, want %v", got, want)
	}
	if _, err := agent.FilterTools(agent.Mode("verify"), available); !errors.Is(err, agent.ErrUnsupportedMode) {
		t.Fatalf("FilterTools unsupported mode error = %v, want ErrUnsupportedMode", err)
	}
}

func TestModeBlockedToolBehavior(t *testing.T) {
	available := []tools.Entry{
		{Name: "read_file", Category: "files", Runtime: "builtin", Safety: tools.Safety{PlanSafe: true}},
		{Name: "glob", Category: "files", Runtime: "builtin", Safety: tools.Safety{PlanSafe: true, ReadsWorkspace: true}},
		{Name: "grep", Category: "files", Runtime: "builtin", Safety: tools.Safety{PlanSafe: true, ReadsWorkspace: true}},
		{Name: "write_file", Category: "files", Runtime: "builtin"},
		{Name: "shell_execute", Category: "command", Runtime: "builtin"},
		{Name: "read_plan", Category: "planning", Runtime: "builtin", Safety: tools.Safety{PlanSafe: true}},
		{Name: "write_plan", Category: "planning", Runtime: "builtin", Safety: tools.Safety{PlanSafe: true, UsesHostState: true, ReadsHostState: true, MutatesHostState: true}},
		{Name: "write_todos", Category: "planning", Runtime: "builtin", Safety: tools.Safety{PlanSafe: true, UsesHostState: true, ReadsHostState: true, MutatesHostState: true}},
		{Name: "read_command_evidence", Category: "planning", Runtime: "builtin", Safety: tools.Safety{PlanSafe: true, UsesHostState: true, ReadsHostState: true}},
		{Name: "submit_plan_implementation", Category: "planning", Runtime: "builtin", Safety: tools.Safety{PlanSafe: true, UsesHostState: true, ReadsHostState: true}},
		{Name: "read_memory", Category: "memory", Runtime: "builtin", Safety: tools.Safety{PlanSafe: true, UsesHostState: true, ReadsHostState: true}},
		{Name: "update_memory", Category: "memory", Runtime: "builtin", Safety: tools.Safety{PlanSafe: true, UsesHostState: true, ReadsHostState: true, MutatesHostState: true}},
	}

	if decision := agent.CheckToolCall(agent.ModePlan, "read_file", available); !decision.Allowed {
		t.Fatalf("read_file decision = %+v, want allowed", decision)
	}
	if decision := agent.CheckToolCall(agent.ModePlan, "glob", available); !decision.Allowed {
		t.Fatalf("glob decision = %+v, want allowed", decision)
	}
	if decision := agent.CheckToolCall(agent.ModePlan, "grep", available); !decision.Allowed {
		t.Fatalf("grep decision = %+v, want allowed", decision)
	}
	if decision := agent.CheckToolCall(agent.ModePlan, "write_plan", available); !decision.Allowed {
		t.Fatalf("write_plan decision = %+v, want allowed", decision)
	}
	if decision := agent.CheckToolCall(agent.ModePlan, "read_memory", available); !decision.Allowed {
		t.Fatalf("read_memory decision = %+v, want allowed", decision)
	}
	if decision := agent.CheckToolCall(agent.ModePlan, "update_memory", available); !decision.Allowed {
		t.Fatalf("update_memory decision = %+v, want allowed", decision)
	}
	for _, name := range []string{"write_file", "shell_execute", "unknown_tool"} {
		decision := agent.CheckToolCall(agent.ModePlan, name, available)
		if decision.Allowed || decision.Tool != name || decision.Reason == "" {
			t.Fatalf("%s decision = %+v, want blocked with reason", name, decision)
		}
	}
	if decision := agent.CheckToolCall(agent.ModeDefault, "write_file", available); !decision.Allowed {
		t.Fatalf("default write_file decision = %+v, want allowed", decision)
	}
	if decision := agent.CheckToolCall(agent.ModeDefault, "read_plan", available); !decision.Allowed {
		t.Fatalf("default read_plan decision = %+v, want allowed", decision)
	}
	if decision := agent.CheckToolCall(agent.ModeDefault, "write_todos", available); !decision.Allowed {
		t.Fatalf("default write_todos decision = %+v, want allowed", decision)
	}
	if decision := agent.CheckToolCall(agent.ModeDefault, "submit_plan_implementation", available); !decision.Allowed {
		t.Fatalf("default submit_plan_implementation decision = %+v, want allowed", decision)
	}
	if decision := agent.CheckToolCall(agent.ModeDefault, "write_plan", available); decision.Allowed {
		t.Fatalf("default write_plan decision = %+v, want blocked", decision)
	}
}

func TestProviderPlanningCategoryIsNotHiddenInDefaultMode(t *testing.T) {
	available := []tools.Entry{
		{Name: "provider_planner", Category: "planning", PackageName: "provider", Safety: tools.Safety{PlanSafe: true}},
		{Name: "core_planner", Category: "planning", PackageName: "falken-core", Safety: tools.Safety{PlanSafe: true}},
		{Name: "provider_writer", Category: "planning", PackageName: "provider", Safety: tools.Safety{MutatesWorkspace: true}},
	}

	defaultTools, err := agent.FilterTools(agent.ModeDefault, available)
	if err != nil {
		t.Fatalf("FilterTools default: %v", err)
	}
	if got, want := toolNames(defaultTools), []string{"provider_planner", "provider_writer"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("default tools = %v, want provider planning-category tools only", got)
	}
	if decision := agent.CheckToolCall(agent.ModeDefault, "provider_planner", available); !decision.Allowed {
		t.Fatalf("provider planner default decision = %+v, want allowed", decision)
	}
	if decision := agent.CheckToolCall(agent.ModeDefault, "core_planner", available); decision.Allowed {
		t.Fatalf("core planner default decision = %+v, want category-based built-in block", decision)
	}

	planTools, err := agent.FilterTools(agent.ModePlan, available)
	if err != nil {
		t.Fatalf("FilterTools plan: %v", err)
	}
	if got, want := toolNames(planTools), []string{"provider_planner", "core_planner"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("plan tools = %v, want plan-safe tools only", got)
	}
	if decision := agent.CheckToolCall(agent.ModePlan, "provider_writer", available); decision.Allowed {
		t.Fatalf("provider writer plan decision = %+v, want workspace mutation blocked", decision)
	}
}

func TestPlanModeAllowsExplicitReadBuiltins(t *testing.T) {
	for _, name := range []string{"read_file", "read_files", "glob", "grep", "read_plan", "read_memory", "update_memory"} {
		decision := agent.IsToolAllowed(agent.ModePlan, tools.Entry{Name: name, Runtime: "builtin", Safety: tools.Safety{PlanSafe: true}})
		if !decision.Allowed {
			t.Fatalf("%s decision = %+v, want allowed", name, decision)
		}
	}
}

func TestPlanModeAllowsWritePlan(t *testing.T) {
	decision := agent.IsToolAllowed(agent.ModePlan, tools.Entry{Name: "write_plan", Runtime: "builtin", Safety: tools.Safety{PlanSafe: true}})
	if !decision.Allowed {
		t.Fatalf("write_plan decision = %+v, want allowed", decision)
	}
}

func TestPlanModeBlocksHostStateMutationWithoutPlanSafe(t *testing.T) {
	entry := tools.Entry{Name: "external_state_writer", Runtime: manifest.RuntimeWasm, Safety: tools.Safety{MutatesHostState: true}}
	decision := agent.IsToolAllowed(agent.ModePlan, entry)
	if decision.Allowed || !strings.Contains(decision.Reason, "host state") {
		t.Fatalf("decision = %+v, want host-state mutation block", decision)
	}
}

func TestPlanModeAllowsExplicitPlanSafeHostStateMutation(t *testing.T) {
	entry := tools.Entry{Name: "external_state_writer", Runtime: manifest.RuntimeWasm, Safety: tools.Safety{PlanSafe: true, ReadsHostState: true, MutatesHostState: true}}
	decision := agent.IsToolAllowed(agent.ModePlan, entry)
	if !decision.Allowed {
		t.Fatalf("decision = %+v, want explicit plan-safe host-state mutation allowed", decision)
	}
}

func TestPlanModeBlocksFileWriteCapability(t *testing.T) {
	entry := tools.Entry{
		Name:    "inspect_workspace",
		Runtime: manifest.RuntimeWasm,
		Permissions: manifest.DeclaredPermissions{
			Files: []manifest.FilePermission{{
				Path:  "notes.txt",
				Match: policy.MatchExact,
				Modes: []policy.FileAccessMode{policy.FileAccessRead, policy.FileAccessWrite},
			}},
		},
	}
	decision := agent.IsToolAllowed(agent.ModePlan, entry)
	if decision.Allowed || !strings.Contains(decision.Reason, "file mutation capability") {
		t.Fatalf("decision = %+v, want file mutation capability block", decision)
	}
}

func TestPlanModeBlocksEmptyFileModesAsBroadCapability(t *testing.T) {
	entry := tools.Entry{
		Name:    "broad_reader",
		Runtime: manifest.RuntimeWasm,
		Permissions: manifest.DeclaredPermissions{
			Files: []manifest.FilePermission{{
				Path:  "src",
				Match: policy.MatchPrefix,
			}},
		},
	}
	decision := agent.IsToolAllowed(agent.ModePlan, entry)
	if decision.Allowed || !strings.Contains(decision.Reason, "broad file capability") {
		t.Fatalf("decision = %+v, want broad file capability block", decision)
	}
}

func TestPlanModeBlocksShellCapability(t *testing.T) {
	entry := tools.Entry{
		Name:    "inspect_workspace",
		Runtime: manifest.RuntimeWasm,
		Permissions: manifest.DeclaredPermissions{
			Shell: []manifest.ShellPermission{{Command: "ls", Match: policy.MatchPrefix}},
		},
	}
	decision := agent.IsToolAllowed(agent.ModePlan, entry)
	if decision.Allowed || !strings.Contains(decision.Reason, "shell capability") {
		t.Fatalf("decision = %+v, want shell capability block", decision)
	}
}

func TestPlanModeDoesNotTrustReadCategoryIfToolHasWritePermission(t *testing.T) {
	entry := tools.Entry{
		Name:     "friendly_reader",
		Category: "read",
		Keywords: []string{"read", "inspect"},
		Runtime:  manifest.RuntimeWasm,
		Permissions: manifest.DeclaredPermissions{
			Files: []manifest.FilePermission{{
				Path:  ".",
				Match: policy.MatchPrefix,
				Modes: []policy.FileAccessMode{policy.FileAccessWrite},
			}},
		},
	}
	decision := agent.IsToolAllowed(agent.ModePlan, entry)
	if decision.Allowed || !strings.Contains(decision.Reason, "file mutation capability") {
		t.Fatalf("decision = %+v, want labels ignored in favor of write capability", decision)
	}
}

func TestPlanModeBlocksUnknownExternalMutatingTool(t *testing.T) {
	entry := tools.Entry{Name: "deploy_changes", Category: "read", Runtime: manifest.RuntimeWasm}
	decision := agent.IsToolAllowed(agent.ModePlan, entry)
	if decision.Allowed || !strings.Contains(decision.Reason, "mutating or command-like") {
		t.Fatalf("decision = %+v, want mutating name block", decision)
	}
}

func TestPlanModeBlocksUndeclaredTool(t *testing.T) {
	entry := tools.Entry{Name: "mystery", Runtime: manifest.RuntimeWasm}
	decision := agent.IsToolAllowed(agent.ModePlan, entry)
	if decision.Allowed || !strings.Contains(decision.Reason, "does not declare safety or permissions") {
		t.Fatalf("decision = %+v, want undeclared tool block", decision)
	}
}

func TestPlanModeAllowsReadOnlySafety(t *testing.T) {
	entry := tools.Entry{Name: "inspect_workspace", Runtime: manifest.RuntimeWasm, Safety: tools.Safety{ReadsWorkspace: true}}
	decision := agent.IsToolAllowed(agent.ModePlan, entry)
	if !decision.Allowed {
		t.Fatalf("decision = %+v, want read-only safety allowed", decision)
	}
}

func TestPlanModeBlockedReasonMentionsCapability(t *testing.T) {
	entry := tools.Entry{
		Name:    "net_reader",
		Runtime: manifest.RuntimeWasm,
		Permissions: manifest.DeclaredPermissions{
			Network: []manifest.NetworkPermission{{Host: "example.com", Match: policy.MatchExact}},
		},
	}
	decision := agent.IsToolAllowed(agent.ModePlan, entry)
	if decision.Allowed || !strings.Contains(decision.Reason, "network capability") {
		t.Fatalf("decision = %+v, want capability reason", decision)
	}
}

func TestPreparedMessagesKeepModeOutOfStableSystemPrompt(t *testing.T) {
	layout := testAgentLayout(t)
	mode := agent.NewModeState(store.NewPlanStore(layout))
	manager := agent.NewHistoryManager(
		store.NewHistoryStore(layout),
		store.NewMemoryStore(layout),
		agent.WithModeState(mode),
	)

	first, err := manager.PrepareRun(agent.PrepareRunRequest{
		BaseSystemPrompt: "Base",
		UserPrompt:       "default prompt",
	})
	if err != nil {
		t.Fatalf("PrepareRun default: %v", err)
	}
	if strings.Contains(first[0].Content, "--- CURRENT MODE ---") || strings.Contains(first[0].Content, "mode: default") {
		t.Fatalf("default stable system prompt contains mode context: %q", first[0].Content)
	}
	if strings.Contains(first[0].Content, "Plan mode is read-only") {
		t.Fatalf("default system prompt should not include plan restrictions: %q", first[0].Content)
	}

	if err := mode.EnterPlan(); err != nil {
		t.Fatalf("EnterPlan: %v", err)
	}
	second, err := manager.PrepareRun(agent.PrepareRunRequest{
		BaseSystemPrompt: "Base",
		UserPrompt:       "plan prompt",
	})
	if err != nil {
		t.Fatalf("PrepareRun plan: %v", err)
	}
	if second[0].Content != first[0].Content {
		t.Fatalf("stable system prompt changed with mode:\ndefault=%q\nplan=%q", first[0].Content, second[0].Content)
	}
	if strings.Contains(second[0].Content, "--- CURRENT MODE ---") || strings.Contains(second[0].Content, "Plan mode is read-only") {
		t.Fatalf("plan stable system prompt contains runtime mode context: %q", second[0].Content)
	}

	if err := mode.Plan().Write(validImplementationPlanForTest()); err != nil {
		t.Fatalf("Write plan: %v", err)
	}
	if err := mode.ExitPlan(); err != nil {
		t.Fatalf("ExitPlan: %v", err)
	}
	third, err := manager.PrepareRun(agent.PrepareRunRequest{
		BaseSystemPrompt: "Base",
		UserPrompt:       "default again",
	})
	if err != nil {
		t.Fatalf("PrepareRun default again: %v", err)
	}
	if strings.Contains(third[0].Content, "Plan mode is read-only") || strings.Contains(third[0].Content, "mode: default") {
		t.Fatalf("default-again stable system prompt contains mode context: %q", third[0].Content)
	}
}

func toolNames(entries []tools.Entry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	return names
}
