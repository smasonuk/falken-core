package falken

import (
	"strings"
	"testing"
)

func TestDefaultSystemPromptDoesNotReferenceUnavailablePlanningTools(t *testing.T) {
	staleNames := []string{
		"TodoWrite",
		"JobCreate",
		"JobList",
		"JobGet",
		"JobUpdate",
		"enter_plan_mode",
		"exit_plan_mode",
	}
	for _, name := range staleNames {
		if strings.Contains(DefaultSystemPrompt, name) {
			t.Fatalf("DefaultSystemPrompt contains stale tool name %q", name)
		}
	}
}

func TestDefaultSystemPromptEnvironmentIsRuntimeNeutral(t *testing.T) {
	for _, stale := range []string{"Docker container", "inside Docker", "sandbox_mount"} {
		if strings.Contains(DefaultSystemPrompt, stale) {
			t.Fatalf("DefaultSystemPrompt contains runtime-specific text %q", stale)
		}
	}
}

func TestDefaultSystemPromptUsesCommandEvidenceModel(t *testing.T) {
	for _, stale := range []string{"verification_" + "intent", "read_" + "verification", "recognizes common verification commands"} {
		if strings.Contains(DefaultSystemPrompt, stale) {
			t.Fatalf("DefaultSystemPrompt contains stale verification model text %q", stale)
		}
	}
	for _, want := range []string{"Falken records recent command evidence", "separate reviewer checks whether the recent commands appear to include reasonable verification"} {
		if !strings.Contains(DefaultSystemPrompt, want) {
			t.Fatalf("DefaultSystemPrompt missing command evidence text %q", want)
		}
	}
}
