package commandtools

import (
	"strings"
	"testing"

	runtimepolicy "github.com/smasonuk/falken-core/internal/policy/runtime"
	"github.com/smasonuk/falken-core/internal/runtimeexec"
)

func TestBuildCommandPayloadConciseErrorAndPolicyFields(t *testing.T) {
	payload := buildCommandPayload(runtimeexec.CommandResult{
		Status:    runtimeexec.CommandStatusExitedNonZero,
		Executed:  true,
		Command:   "false",
		ExitCode:  1,
		Stderr:    strings.Repeat("stderr\n", 100),
		ExitError: "exit status 1",
		Policy: runtimepolicy.ShellResult{
			Outcome:                   runtimepolicy.ShellOutcomeAllowed,
			BlockedByShellWriteBypass: false,
		},
	})

	if payload["error"] != "exit status 1" {
		t.Fatalf("error = %#v, want concise exit error", payload["error"])
	}
	policy, ok := payload["policy"].(map[string]any)
	if !ok {
		t.Fatalf("policy = %#v, want object", payload["policy"])
	}
	if policy["outcome"] != string(runtimepolicy.ShellOutcomeAllowed) {
		t.Fatalf("policy = %+v, want outcome field", policy)
	}
	for _, legacyField := range []string{
		"mutates_workspace",
		"purely_" + "observational",
		"unsupported",
		"requires_" + "destructive_handling",
		"potentially_" + "mutates_workspace",
	} {
		if _, ok := policy[legacyField]; ok {
			t.Fatalf("policy = %+v, should not expose %s", policy, legacyField)
		}
	}
	if payload["policy_outcome"] != string(runtimepolicy.ShellOutcomeAllowed) {
		t.Fatalf("policy_outcome = %#v, want backward-compatible root field", payload["policy_outcome"])
	}
}

func TestBuildCommandContentMarksRemoteArtifactsAsAgentState(t *testing.T) {
	content := buildCommandContent(runtimeexec.CommandResult{
		Status:   runtimeexec.CommandStatusSucceeded,
		Executed: true,
		Command:  "printf lots",
		ExitCode: 0,
		Output: runtimeexec.OutputSummary{
			Truncated:                   true,
			ArtifactPath:                "/state/artifacts/recent/command-output.txt",
			ArtifactWorkspaceAccessible: false,
			OriginalBytes:               1024,
			PreviewBytes:                64,
			InlineLimit:                 64,
		},
		Policy: runtimepolicy.ShellResult{Outcome: runtimepolicy.ShellOutcomeAllowed, Allowed: true},
	})

	if !strings.Contains(content, "not the workspace") {
		t.Fatalf("content = %q, want remote artifact limitation", content)
	}
	if strings.Contains(strings.ToLower(content), "grep") {
		t.Fatalf("content = %q, should not instruct grep for agent-state artifact", content)
	}
}
