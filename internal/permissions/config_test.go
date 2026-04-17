package permissions

import (
	"os"
	"reflect"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	configPath := ".falken.yaml"
	defer os.Remove(configPath)

	t.Run("DefaultConfig", func(t *testing.T) {
		os.Remove(configPath)
		cfg, err := LoadConfig()
		if err != nil {
			t.Fatalf("LoadConfig failed: %v", err)
		}
		if cfg.SandboxImage != "falken/sandbox:latest" {
			t.Errorf("expected default sandbox image, got %s", cfg.SandboxImage)
		}
		if !cfg.ShowPermissionOverview {
			t.Error("expected default ShowPermissionOverview to be true")
		}
	})

	t.Run("ExplicitEmptyGlobalBlockedFiles", func(t *testing.T) {
		err := os.WriteFile(configPath, []byte("global_blocked_files: []"), 0644)
		if err != nil {
			t.Fatalf("failed to write config: %v", err)
		}
		cfg, err := LoadConfig()
		if err != nil {
			t.Fatalf("LoadConfig failed: %v", err)
		}
		if cfg.GlobalBlockedFiles == nil {
			t.Fatal("GlobalBlockedFiles should not be nil")
		}
		if len(cfg.GlobalBlockedFiles) != 0 {
			t.Errorf("expected empty global blocked files, got %v", cfg.GlobalBlockedFiles)
		}
	})

	t.Run("CustomGlobalBlockedFiles", func(t *testing.T) {
		err := os.WriteFile(configPath, []byte("global_blocked_files: [\"custom.txt\"]"), 0644)
		if err != nil {
			t.Fatalf("failed to write config: %v", err)
		}
		cfg, err := LoadConfig()
		if err != nil {
			t.Fatalf("LoadConfig failed: %v", err)
		}
		expected := []string{"custom.txt"}
		if !reflect.DeepEqual(cfg.GlobalBlockedFiles, expected) {
			t.Errorf("expected %v, got %v", expected, cfg.GlobalBlockedFiles)
		}
	})

	t.Run("LegacyProjectDotfilesMigratesToPersistentAllowedFiles", func(t *testing.T) {
		err := os.WriteFile(configPath, []byte("project_dotfiles:\n  .env: read/write\n"), 0644)
		if err != nil {
			t.Fatalf("failed to write config: %v", err)
		}
		cfg, err := LoadConfig()
		if err != nil {
			t.Fatalf("LoadConfig failed: %v", err)
		}
		if got := cfg.PersistentAllowedFiles[".env"]; got != "read/write" {
			t.Fatalf("expected migrated persistent file approval, got %q", got)
		}
	})
}
