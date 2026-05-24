package store

import (
	"fmt"
	"os"

	"github.com/smasonuk/falken-core/internal/persist"
	"github.com/smasonuk/falken-core/internal/state"
)

// CommandEvidenceRecord is durable objective evidence for one command result.
type CommandEvidenceRecord struct {
	Command    string `json:"command"`
	WorkingDir string `json:"working_dir,omitempty"`
	Status     string `json:"status"`
	ExitCode   int    `json:"exit_code"`
	Executed   bool   `json:"executed"`
	Succeeded  bool   `json:"succeeded"`
	RecordedAt string `json:"recorded_at"`
	Revision   int64  `json:"revision,omitempty"`

	OutputTruncated     bool `json:"output_truncated,omitempty"`
	OutputOriginalBytes int  `json:"output_original_bytes,omitempty"`
	OutputPreviewBytes  int  `json:"output_preview_bytes,omitempty"`

	PolicyOutcome        string `json:"policy_outcome,omitempty"`
	PolicyExplanation    string `json:"policy_explanation,omitempty"`
	HasShellWritePattern bool   `json:"has_shell_write_pattern,omitempty"`
}

// CommandEvidenceReview is the persisted structured reviewer result.
type CommandEvidenceReview struct {
	Verdict               string   `json:"verdict"`
	VerificationPerformed bool     `json:"verification_performed"`
	Confidence            string   `json:"confidence"`
	Reason                string   `json:"reason"`
	RelevantCommands      []string `json:"relevant_commands,omitempty"`
	SuggestedNextStep     string   `json:"suggested_next_step,omitempty"`
	Warning               string   `json:"warning,omitempty"`
	RecordedAt            string   `json:"recorded_at,omitempty"`
}

// CommandEvidenceState persists runtime command evidence and reviewer state.
type CommandEvidenceState struct {
	Records []CommandEvidenceRecord `json:"records"`

	ReviewAttempts int                    `json:"review_attempts,omitempty"`
	LastReview     *CommandEvidenceReview `json:"last_review,omitempty"`

	PlanBaselineRevision          int64  `json:"plan_baseline_revision,omitempty"`
	LastWorkspaceMutationRevision int64  `json:"last_workspace_mutation_revision,omitempty"`
	LastWorkspaceMutationAt       string `json:"last_workspace_mutation_at,omitempty"`
	LastWorkspaceMutationTool     string `json:"last_workspace_mutation_tool,omitempty"`
}

// CommandEvidenceStore persists conversation-scoped command evidence.
type CommandEvidenceStore struct {
	path       string
	legacyPath string
}

// NewCommandEvidenceStore builds a command evidence store from the canonical state layout.
func NewCommandEvidenceStore(layout state.Layout) CommandEvidenceStore {
	return CommandEvidenceStore{
		path:       layout.CommandEvidencePath,
		legacyPath: layout.VerificationPath,
	}
}

// Path returns the canonical backing file path for the command evidence store.
func (s CommandEvidenceStore) Path() string {
	return s.path
}

// LegacyPath returns the previous backing file path used before command evidence was renamed.
func (s CommandEvidenceStore) LegacyPath() string {
	return s.legacyPath
}

// Read loads command evidence state. A missing file returns an empty state.
func (s CommandEvidenceStore) Read() (CommandEvidenceState, error) {
	evidence, found, err := s.readPath(s.path)
	if err != nil {
		return CommandEvidenceState{}, err
	}
	if found {
		return normalizeCommandEvidenceState(evidence), nil
	}
	if s.legacyPath != "" && s.legacyPath != s.path {
		evidence, found, err = s.readPath(s.legacyPath)
		if err != nil {
			return CommandEvidenceState{}, err
		}
		if found {
			return normalizeCommandEvidenceState(evidence), nil
		}
	}
	return emptyCommandEvidenceState(), nil
}

func (s CommandEvidenceStore) readPath(path string) (CommandEvidenceState, bool, error) {
	if path == "" {
		return emptyCommandEvidenceState(), false, nil
	}
	var evidence CommandEvidenceState
	found, err := persist.ReadJSON(path, &evidence)
	if err != nil {
		return CommandEvidenceState{}, false, fmt.Errorf("read command evidence store: %w", err)
	}
	return evidence, found, nil
}

// Write persists the complete command evidence state atomically.
func (s CommandEvidenceStore) Write(evidence CommandEvidenceState) error {
	evidence = normalizeCommandEvidenceState(evidence)
	if err := persist.WriteJSONAtomic(s.path, evidence, 0o600); err != nil {
		return fmt.Errorf("write command evidence store: %w", err)
	}
	return nil
}

// Remove deletes both canonical and legacy command evidence files if present.
func (s CommandEvidenceStore) Remove() error {
	for _, path := range []string{s.path, s.legacyPath} {
		if path == "" {
			continue
		}
		err := os.Remove(path)
		if err == nil || os.IsNotExist(err) {
			continue
		}
		return fmt.Errorf("remove command evidence store %q: %w", path, err)
	}
	return nil
}

func emptyCommandEvidenceState() CommandEvidenceState {
	return CommandEvidenceState{Records: []CommandEvidenceRecord{}}
}

func normalizeCommandEvidenceState(evidence CommandEvidenceState) CommandEvidenceState {
	if evidence.Records == nil {
		evidence.Records = []CommandEvidenceRecord{}
	}
	return evidence
}
