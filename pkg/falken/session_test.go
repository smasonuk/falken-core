package falken

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/smasonuk/falken-core/internal/agent"
	"github.com/smasonuk/falken-core/internal/extensions"
	"github.com/smasonuk/falken-core/internal/extensions/manifest"
	"github.com/smasonuk/falken-core/internal/runtimeapi"

	openai "github.com/sashabaranov/go-openai"
)

type fakeResourceSet struct {
	calls int
}

func (f *fakeResourceSet) Close(context.Context) error {
	f.calls++
	return nil
}

func TestNewSessionRespectsExplicitWorkspaceAndStateDirs(t *testing.T) {
	workspace := t.TempDir()
	stateDir := t.TempDir()

	session, err := NewSession(Config{
		Client:       openai.NewClient("test-key"),
		ModelName:    "gpt-5.2",
		SystemPrompt: "test system prompt",
		WorkspaceDir: workspace,
		StateDir:     stateDir,
		Logger:       log.Default(),
		ToolDir:      filepath.Join(workspace, "tools"),
		PluginDir:    filepath.Join(workspace, "plugins"),
	})
	if err != nil {
		t.Fatalf("NewSession returned error: %v", err)
	}

	paths := session.Paths()
	if paths.WorkspaceDir != workspace {
		t.Fatalf("expected workspace %q, got %q", workspace, paths.WorkspaceDir)
	}
	if paths.StateDir != stateDir {
		t.Fatalf("expected state dir %q, got %q", stateDir, paths.StateDir)
	}

	if err := session.Close(context.Background()); err != nil {
		t.Fatalf("Close should be safe before Start: %v", err)
	}
}

func TestNewSessionResolvesRelativeExtensionDirsFromWorkspace(t *testing.T) {
	workspace := t.TempDir()
	otherDir := t.TempDir()

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	if err := os.Chdir(otherDir); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}
	defer os.Chdir(oldWD)

	session, err := NewSession(Config{
		Client:       openai.NewClient("test-key"),
		ModelName:    "gpt-5.2",
		SystemPrompt: "test system prompt",
		WorkspaceDir: workspace,
		Logger:       log.Default(),
		ToolDir:      "tools",
		PluginDir:    "plugins",
	})
	if err != nil {
		t.Fatalf("NewSession returned error: %v", err)
	}
	defer session.Close(context.Background())

	if session.cfg.ToolDir != filepath.Join(workspace, "tools") {
		t.Fatalf("expected tool dir under workspace, got %q", session.cfg.ToolDir)
	}
	if session.cfg.PluginDir != filepath.Join(workspace, "plugins") {
		t.Fatalf("expected plugin dir under workspace, got %q", session.cfg.PluginDir)
	}
}

func TestSessionCloseClosesExtensionResources(t *testing.T) {
	toolRT := &fakeResourceSet{}
	plugRT := &fakeResourceSet{}

	session := &Session{
		toolRT: toolRT,
		plugRT: plugRT,
	}

	if err := session.Close(context.Background()); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if toolRT.calls != 1 {
		t.Fatalf("expected tool resources to close once, got %d", toolRT.calls)
	}
	if plugRT.calls != 1 {
		t.Fatalf("expected plugin resources to close once, got %d", plugRT.calls)
	}
}

func TestSessionPluginInfosAggregatesManifestMetadata(t *testing.T) {
	session := &Session{
		plugins: []extensions.WasmHook{
			{
				PluginName:  "alpha",
				Description: "Alpha plugin",
				Permissions: manifest.GranularPermissions{
					Network: []manifest.NetworkRule{
						{Domain: "example.com"},
						{URL: "https://api.example.com"},
					},
					Shell: []string{"git status"},
					Files: []manifest.FileAccess{
						{Path: "/tmp/a", Access: "read"},
					},
				},
			},
			{
				PluginName:  "alpha",
				Description: "Alpha plugin",
				Permissions: manifest.GranularPermissions{
					Network: []manifest.NetworkRule{
						{Domain: "example.com"},
						{Domain: "example.org"},
					},
					Shell: []string{"git status", "git diff"},
					Files: []manifest.FileAccess{
						{Path: "/tmp/a", Access: "read"},
						{Path: "/tmp/b", Access: "write"},
					},
				},
			},
			{
				PluginName:  "beta",
				Description: "Beta plugin",
				Internal:    true,
			},
		},
	}

	got := session.PluginInfos()
	want := []PluginInfo{
		{
			Name:            "alpha",
			Description:     "Alpha plugin",
			Internal:        false,
			NetworkTargets:  []string{"example.com", "example.org", "https://api.example.com"},
			ShellCommands:   []string{"git diff", "git status"},
			FilePermissions: []string{"/tmp/a (read)", "/tmp/b (write)"},
		},
		{
			Name:        "beta",
			Description: "Beta plugin",
			Internal:    true,
		},
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected plugin infos:\n got %#v\nwant %#v", got, want)
	}
}

func TestSessionResetConversationStateRemovesPersistedState(t *testing.T) {
	workspace := t.TempDir()
	paths, err := runtimeapi.NewPaths(workspace, "")
	if err != nil {
		t.Fatalf("NewPaths failed: %v", err)
	}
	if err := paths.EnsureStateDirs(); err != nil {
		t.Fatalf("EnsureStateDirs failed: %v", err)
	}

	for _, path := range []string{
		paths.HistoryPath(),
		paths.MemoryPath(),
		paths.TodosPath(),
		paths.TasksPath(),
		filepath.Join(paths.TasksDir(), "1", "result.md"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatalf("MkdirAll failed: %v", err)
		}
		if err := os.WriteFile(path, []byte("state"), 0644); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}
	}

	session := &Session{
		paths: paths,
		runner: &agent.Runner{
			History: []openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "old"}},
		},
	}

	if err := session.ResetConversationState(); err != nil {
		t.Fatalf("ResetConversationState failed: %v", err)
	}
	if len(session.runner.History) != 0 {
		t.Fatalf("expected in-memory history to be cleared, got %#v", session.runner.History)
	}

	for _, path := range []string{
		paths.HistoryPath(),
		paths.MemoryPath(),
		paths.TodosPath(),
		paths.TasksPath(),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("expected %s to be removed, got err=%v", path, err)
		}
	}
	if _, err := os.Stat(paths.TasksDir()); !os.IsNotExist(err) {
		t.Fatalf("expected tasks dir to be removed, got err=%v", err)
	}
}
