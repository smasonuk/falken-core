package agent

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/smasonuk/falken-core/internal/conversation"
	"github.com/smasonuk/falken-core/internal/extensions/tools"
	"github.com/smasonuk/falken-core/internal/policy"
	"github.com/smasonuk/falken-core/internal/store"
)

const (
	modeSectionHeader    = "--- CURRENT MODE ---"
	planModeRestrictions = "Plan mode may read workspace files and may mutate Falken internal conversation state through explicitly plan-safe host-state tools. Do not execute commands, access the network, or mutate workspace files."
)

var (
	// ErrUnsupportedMode indicates a mode value outside the v1 mode set.
	ErrUnsupportedMode = errors.New("unsupported mode")
)

// Mode identifies the v1 agent runtime mode.
type Mode string

const (
	ModeDefault Mode = "default"
	ModePlan    Mode = "plan"
)

// ModeState tracks current runtime mode and delegates plan persistence to PlanManager.
type ModeState struct {
	mu   sync.RWMutex
	mode Mode
	plan *conversation.PlanManager
}

// NewModeState creates explicit v1 mode state. The initial mode is Default.
func NewModeState(planStore store.PlanBackend) *ModeState {
	return &ModeState{
		mode: ModeDefault,
		plan: conversation.NewPlanManager(planStore),
	}
}

// Current returns the current runtime mode.
func (s *ModeState) Current() Mode {
	if s == nil {
		return ModeDefault
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.mode
}

// EnterPlan switches into Plan mode and initializes starter plan content if no plan exists.
func (s *ModeState) EnterPlan() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.plan != nil {
		plan, err := s.plan.Read()
		if err != nil {
			return err
		}
		if strings.TrimSpace(plan) == "" {
			if err := s.plan.Write(conversation.DefaultPlanStarterText); err != nil {
				return err
			}
		}
	}

	s.mode = ModePlan
	return nil
}

// ExitPlan validates current plan content and returns to Default mode.
func (s *ModeState) ExitPlan() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mode != ModePlan {
		return nil
	}
	if s.plan != nil {
		plan, err := s.plan.Read()
		if err != nil {
			return err
		}
		if err := ValidatePlan(plan); err != nil {
			return err
		}
	}

	s.mode = ModeDefault
	return nil
}

// CompletePlanAfterWrite returns to default mode after write_plan has already
// validated and persisted a complete implementation plan.
func (s *ModeState) CompletePlanAfterWrite() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mode != ModePlan {
		return nil
	}

	s.mode = ModeDefault
	return nil
}

// Reset returns mode state to Default without changing persisted plan content.
func (s *ModeState) Reset() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mode = ModeDefault
}

// Plan returns the mode state's plan helper.
func (s *ModeState) Plan() *conversation.PlanManager {
	if s == nil {
		return nil
	}
	return s.plan
}

// ToolPolicyDecision reports whether a tool is allowed in the current mode.
type ToolPolicyDecision struct {
	Allowed bool
	Mode    Mode
	Tool    string
	Reason  string
}

// FilterTools returns the tools allowed by the current mode policy in deterministic input order.
func FilterTools(mode Mode, available []tools.Entry) ([]tools.Entry, error) {
	if mode == ModeDefault {
		allowed := make([]tools.Entry, 0, len(available))
		for _, entry := range available {
			if isPlanningTool(entry) && !planningStateToolAllowedInDefault(entry.Name) {
				continue
			}
			allowed = append(allowed, tools.CloneEntry(entry))
		}
		return allowed, nil
	}
	if mode != ModePlan {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedMode, mode)
	}

	allowed := make([]tools.Entry, 0, len(available))
	for _, entry := range available {
		if IsToolAllowed(mode, entry).Allowed {
			allowed = append(allowed, tools.CloneEntry(entry))
		}
	}

	return allowed, nil
}

// IsToolAllowed evaluates one active tool against the current mode policy.
func IsToolAllowed(mode Mode, entry tools.Entry) ToolPolicyDecision {
	if mode == ModeDefault {
		if isPlanningTool(entry) && !planningStateToolAllowedInDefault(entry.Name) {
			return ToolPolicyDecision{Allowed: false, Mode: mode, Tool: entry.Name, Reason: "planning tools are only available in plan mode"}
		}
		return ToolPolicyDecision{Allowed: true, Mode: mode, Tool: entry.Name, Reason: "default mode allows active tools"}
	}
	if mode != ModePlan {
		return ToolPolicyDecision{Allowed: false, Mode: mode, Tool: entry.Name, Reason: "unsupported mode"}
	}

	decision := planModeToolDecision(entry)
	decision.Mode = mode
	return decision
}

// CheckToolCall reports whether an attempted tool call is blocked by the current mode.
func CheckToolCall(mode Mode, toolName string, available []tools.Entry) ToolPolicyDecision {
	for _, entry := range available {
		if entry.Name == toolName {
			return IsToolAllowed(mode, entry)
		}
	}
	if mode == ModeDefault {
		if isPlanningTool(tools.Entry{Name: toolName}) && !planningStateToolAllowedInDefault(toolName) {
			return ToolPolicyDecision{Allowed: false, Mode: mode, Tool: toolName, Reason: "planning tools are only available in plan mode"}
		}
		return ToolPolicyDecision{Allowed: true, Mode: mode, Tool: toolName, Reason: "default mode allows active tools"}
	}

	return ToolPolicyDecision{
		Allowed: false,
		Mode:    mode,
		Tool:    toolName,
		Reason:  "tool is not known to be read/planning-safe in plan mode",
	}
}

// RenderMode renders mode context for the system prompt.
func RenderMode(mode Mode) string {
	switch mode {
	case ModePlan:
		return modeSectionHeader + "\nmode: plan\n" + planModeRestrictions
	case ModeDefault:
		return modeSectionHeader + "\nmode: default"
	default:
		return modeSectionHeader + "\nmode: " + string(mode) + "\nUnsupported mode; default to conservative runtime behavior."
	}
}

func planModeToolDecision(entry tools.Entry) ToolPolicyDecision {
	name := normalizePolicyTerm(entry.Name)
	decision := ToolPolicyDecision{Allowed: false, Mode: ModePlan, Tool: entry.Name}
	if entry.Safety.MutatesWorkspace {
		decision.Reason = "tool declares file mutation capability and is blocked in plan mode"
		return decision
	}
	if reason := fileCapabilityBlockReason(entry); reason != "" {
		decision.Reason = reason
		return decision
	}
	if entry.Safety.ExecutesShell || len(entry.Permissions.Shell) != 0 {
		decision.Reason = "tool declares shell capability and is blocked in plan mode"
		return decision
	}
	if entry.Safety.UsesNetwork || len(entry.Permissions.Network) != 0 {
		decision.Reason = "tool declares network capability and is blocked in plan mode"
		return decision
	}
	if entry.Safety.MutatesHostState && !entry.Safety.PlanSafe {
		decision.Reason = "tool mutates host state but is not explicitly plan-safe"
		return decision
	}
	if entry.Safety.PlanSafe {
		decision.Allowed = true
		decision.Reason = "tool declares plan-safe behavior"
		return decision
	}
	if planBlockedToolName(name) {
		decision.Reason = "tool name is mutating or command-like and is blocked in plan mode"
		return decision
	}
	if entry.Safety == (tools.Safety{}) && noPermissions(entry) {
		decision.Reason = "tool does not declare safety or permissions"
		return decision
	}
	decision.Allowed = true
	decision.Reason = "tool declares no mutating, shell, or network capabilities"
	return decision
}

func isPlanningTool(entry tools.Entry) bool {
	name := normalizePolicyTerm(entry.Name)
	switch name {
	case "read_plan", "write_plan", "read_todos", "write_todos", "read_command_evidence", "submit_plan_implementation":
		return true
	default:
		return entry.PackageName == "falken-core" && strings.EqualFold(entry.Category, "planning")
	}
}

func planningStateToolAllowedInDefault(toolName string) bool {
	switch normalizePolicyTerm(toolName) {
	case "read_plan", "read_todos", "write_todos", "read_command_evidence", "submit_plan_implementation":
		return true
	default:
		return false
	}
}

func fileCapabilityBlockReason(entry tools.Entry) string {
	for _, file := range entry.Permissions.Files {
		if len(file.Modes) == 0 {
			return "tool declares broad file capability and is blocked in plan mode"
		}
		for _, mode := range file.Modes {
			switch mode {
			case policy.FileAccessWrite, policy.FileAccessCreate:
				return "tool declares file mutation capability and is blocked in plan mode"
			}
		}
	}
	return ""
}

func noPermissions(entry tools.Entry) bool {
	return len(entry.Permissions.Files) == 0 && len(entry.Permissions.Shell) == 0 && len(entry.Permissions.Network) == 0
}

func planBlockedToolName(name string) bool {
	blockedFragments := []string{
		"write",
		"create",
		"overwrite",
		"edit",
		"patch",
		"delete",
		"remove",
		"shell",
		"command",
		"exec",
		"run_command",
		"background",
		"process",
		"deploy",
	}
	for _, fragment := range blockedFragments {
		if strings.Contains(name, fragment) {
			return true
		}
	}

	return false
}

func normalizePolicyTerm(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	normalized = strings.ReplaceAll(normalized, " ", "_")
	return normalized
}
