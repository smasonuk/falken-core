package state

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	// LayoutVersion is the canonical Falken state layout version.
	LayoutVersion = 1
	appName       = "falken"
)

// Layout captures the canonical runtime paths for Falken state.
type Layout struct {
	WorkspaceRoot           string
	StateRoot               string
	MetadataPath            string
	CurrentConversationRoot string
	HistoryPath             string
	MemoryPath              string
	TodosPath               string
	PlanPath                string
	CommandEvidencePath     string
	// Deprecated: legacy name retained only for migration.
	VerificationPath     string
	CacheRoot            string
	BackupRoot           string
	TruncationRoot       string
	RecentTruncationRoot string
	ArtifactRoot         string
	RecentArtifactRoot   string
	PluginStateRoot      string
	LayoutVersion        int
}

// ResolveLayout builds the canonical path layout for a workspace and optional explicit state root.
func ResolveLayout(workspaceRoot, explicitStateRoot string) (Layout, error) {
	stateRoot, err := resolveStateRoot(workspaceRoot, explicitStateRoot)
	if err != nil {
		return Layout{}, err
	}

	return Layout{
		WorkspaceRoot:           workspaceRoot,
		StateRoot:               stateRoot,
		MetadataPath:            filepath.Join(stateRoot, "metadata.json"),
		CurrentConversationRoot: filepath.Join(stateRoot, "conversations", "current"),
		HistoryPath:             filepath.Join(stateRoot, "conversations", "current", "history.json"),
		MemoryPath:              filepath.Join(stateRoot, "conversations", "current", "memory.json"),
		TodosPath:               filepath.Join(stateRoot, "conversations", "current", "todos.json"),
		PlanPath:                filepath.Join(stateRoot, "conversations", "current", "plan.json"),
		CommandEvidencePath:     filepath.Join(stateRoot, "conversations", "current", "command_evidence.json"),
		VerificationPath:        filepath.Join(stateRoot, "conversations", "current", "verification.json"),
		CacheRoot:               filepath.Join(stateRoot, "cache"),
		BackupRoot:              filepath.Join(stateRoot, "backups"),
		TruncationRoot:          filepath.Join(stateRoot, "truncations"),
		RecentTruncationRoot:    filepath.Join(stateRoot, "truncations", "recent"),
		ArtifactRoot:            filepath.Join(stateRoot, "artifacts"),
		RecentArtifactRoot:      filepath.Join(stateRoot, "artifacts", "recent"),
		PluginStateRoot:         filepath.Join(stateRoot, "plugins"),
		LayoutVersion:           LayoutVersion,
	}, nil
}

func resolveStateRoot(workspaceRoot, explicitStateRoot string) (string, error) {
	if explicitStateRoot != "" {
		return cleanAbs(explicitStateRoot)
	}

	stateHome, err := userStateHome()
	if err != nil {
		return "", err
	}

	workspaceID := workspaceStateID(workspaceRoot)
	return filepath.Join(stateHome, "workspaces", workspaceID), nil
}

func userStateHome() (string, error) {
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return cleanAbs(filepath.Join(dir, appName))
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home for state root: %w", err)
	}

	switch runtime.GOOS {
	case "darwin":
		return cleanAbs(filepath.Join(home, "Library", "Application Support", appName, "state"))
	case "windows":
		if dir := os.Getenv("LOCALAPPDATA"); dir != "" {
			return cleanAbs(filepath.Join(dir, "Falken", "State"))
		}

		configDir, err := os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("resolve user config dir for state root: %w", err)
		}

		return cleanAbs(filepath.Join(configDir, "Falken", "State"))
	default:
		return cleanAbs(filepath.Join(home, ".local", "state", appName))
	}
}

func cleanAbs(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path %q: %w", path, err)
	}

	return filepath.Clean(abs), nil
}

func workspaceStateID(workspaceRoot string) string {
	sum := sha256.Sum256([]byte(workspaceRoot))
	hash := hex.EncodeToString(sum[:8])
	base := slug(filepath.Base(workspaceRoot))
	if base == "" {
		base = "workspace"
	}

	return base + "-" + hash
}

func slug(name string) string {
	name = strings.ToLower(name)

	var b strings.Builder
	lastDash := false

	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
			lastDash = false
		case r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == '-' || r == '_':
			if !lastDash && b.Len() > 0 {
				b.WriteRune(r)
				lastDash = true
			}
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}

	return strings.Trim(b.String(), "-_")
}
