package tools_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/smasonuk/falken-core/internal/extensions/manifest"
	"github.com/smasonuk/falken-core/internal/extensions/tools"
	"github.com/smasonuk/falken-core/internal/policy"
)

func TestDiscover_MissingAndEmptyRoots(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	registry, result, err := tools.Discover(tools.DiscoverOptions{Roots: []string{missing}})
	if err != nil {
		t.Fatalf("Discover missing: %v", err)
	}
	if registry.Count() != 0 || len(result.Registered) != 0 || len(result.Invalid) != 0 {
		t.Fatalf("missing root registry/result = %d/%+v, want no tools", registry.Count(), result)
	}
	if len(result.Skipped) != 1 || result.Skipped[0].Reason != "tool root does not exist" {
		t.Fatalf("skipped = %+v, want missing root skip", result.Skipped)
	}

	empty := t.TempDir()
	registry, result, err = tools.Discover(tools.DiscoverOptions{Roots: []string{empty}})
	if err != nil {
		t.Fatalf("Discover empty: %v", err)
	}
	if registry.Count() != 0 || len(result.Registered) != 0 || len(result.Invalid) != 0 || len(result.Skipped) != 0 {
		t.Fatalf("empty root registry/result = %d/%+v, want empty result", registry.Count(), result)
	}
}

func TestDiscover_ValidSingleToolManifest(t *testing.T) {
	root := t.TempDir()
	packageDir := writeToolPackage(t, root, "notes", validToolManifest("note_reader", "notes-tools"))
	registry, result, err := tools.Discover(tools.DiscoverOptions{Roots: []string{root}})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	if len(result.Registered) != 1 || registry.Count() != 1 {
		t.Fatalf("registered count/result = %d/%+v, want 1", registry.Count(), result)
	}
	entry, found := registry.Lookup("note_reader")
	if !found {
		t.Fatal("expected lookup for note_reader")
	}
	if entry.PackageName != "notes-tools" || entry.SourceDir != packageDir {
		t.Fatalf("entry package/source = %q/%q", entry.PackageName, entry.SourceDir)
	}
	if entry.ManifestPath != filepath.Join(packageDir, tools.ManifestFileName) {
		t.Fatalf("manifest path = %q, want convention path", entry.ManifestPath)
	}
	if entry.WasmPath != filepath.Join(packageDir, tools.WasmFileName) {
		t.Fatalf("wasm path = %q, want convention path", entry.WasmPath)
	}
	if entry.Runtime != manifest.RuntimeWasm || entry.ManifestVersion != manifest.SupportedManifestVersion {
		t.Fatalf("runtime/version = %q/%d", entry.Runtime, entry.ManifestVersion)
	}
	if len(entry.Permissions.Files) != 1 || entry.Permissions.Files[0].Path != "notes/" {
		t.Fatalf("permissions = %+v, want preserved file permissions", entry.Permissions)
	}
	if !entry.Safety.ReadsWorkspace || entry.Safety.MutatesWorkspace || entry.Safety.ExecutesShell || entry.Safety.UsesNetwork {
		t.Fatalf("safety = %+v, want read-only workspace safety", entry.Safety)
	}
}

func TestDiscover_DerivesSafetyFromManifestPermissions(t *testing.T) {
	tests := []struct {
		name string
		body string
		want tools.Safety
	}{
		{
			name: "read file",
			body: manifestWithPermissions(`"files":[{"path":"notes/","match":"prefix","modes":["read"]}]`),
			want: tools.Safety{ReadsWorkspace: true},
		},
		{
			name: "write file",
			body: manifestWithPermissions(`"files":[{"path":"notes/","match":"prefix","modes":["write"]}]`),
			want: tools.Safety{ReadsWorkspace: true, MutatesWorkspace: true},
		},
		{
			name: "broad file",
			body: manifestWithPermissions(`"files":[{"path":"notes/","match":"prefix"}]`),
			want: tools.Safety{ReadsWorkspace: true, MutatesWorkspace: true},
		},
		{
			name: "shell and network",
			body: manifestWithPermissions(`"shell":[{"command":"go test","match":"prefix"}],"network":[{"host":"example.com","match":"exact"}]`),
			want: tools.Safety{ExecutesShell: true, UsesNetwork: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeToolPackage(t, root, "pkg", tt.body)
			registry, result, err := tools.Discover(tools.DiscoverOptions{Roots: []string{root}})
			if err != nil {
				t.Fatalf("Discover: %v", err)
			}
			if len(result.Invalid) != 0 {
				t.Fatalf("invalid = %+v", result.Invalid)
			}
			entry, ok := registry.Lookup("perm_tool")
			if !ok {
				t.Fatal("missing perm_tool")
			}
			if entry.Safety != tt.want {
				t.Fatalf("safety = %+v, want %+v", entry.Safety, tt.want)
			}
		})
	}
}

func TestDiscover_ValidMultiToolManifestAndLoadMetadata(t *testing.T) {
	root := t.TempDir()
	packageDir := writeToolPackage(t, root, "multi", `{
		"manifest_version": 1,
		"name": "multi-tools",
		"description": "Multiple tools.",
		"runtime": "wasm",
		"tools": [
			{
				"name": "alpha",
				"description": "Alpha tool.",
				"input_schema": {"type": "object"},
				"category": "planning",
				"keywords": ["a", "plan"],
				"always_load": true
			},
			{
				"name": "beta",
				"description": "Beta tool.",
				"input_schema": {"type": "object"},
				"default_load": true
			}
		]
	}`)

	registry, result, err := tools.Discover(tools.DiscoverOptions{Roots: []string{root}})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(result.Registered) != 2 || registry.Count() != 2 {
		t.Fatalf("registered count/result = %d/%+v, want 2", registry.Count(), result)
	}

	list := registry.List()
	if got := []string{list[0].Name, list[1].Name}; !reflect.DeepEqual(got, []string{"alpha", "beta"}) {
		t.Fatalf("list order = %v, want alpha,beta", got)
	}
	alpha, _ := registry.Lookup("alpha")
	if !alpha.AlwaysLoad || alpha.DefaultLoad || alpha.Category != "planning" {
		t.Fatalf("alpha metadata = %+v, want always-load planning metadata", alpha)
	}
	if !reflect.DeepEqual(alpha.Keywords, []string{"a", "plan"}) {
		t.Fatalf("alpha keywords = %v", alpha.Keywords)
	}
	beta, _ := registry.Lookup("beta")
	if !beta.DefaultLoad || beta.AlwaysLoad {
		t.Fatalf("beta metadata = %+v, want default-load metadata", beta)
	}
	if beta.SourceDir != packageDir {
		t.Fatalf("beta source dir = %q, want %q", beta.SourceDir, packageDir)
	}
}

func TestDiscover_SkipsNonToolEntries(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("not a package"), 0o600); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "empty-package"), 0o755); err != nil {
		t.Fatalf("mkdir empty package: %v", err)
	}

	registry, result, err := tools.Discover(tools.DiscoverOptions{Roots: []string{root}})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if registry.Count() != 0 || len(result.Registered) != 0 || len(result.Invalid) != 0 {
		t.Fatalf("registry/result = %d/%+v, want no registered or invalid tools", registry.Count(), result)
	}
	if len(result.Skipped) != 2 {
		t.Fatalf("skipped = %+v, want README and missing-manifest package", result.Skipped)
	}
}

func TestDiscover_InvalidManifestsReported(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantError string
	}{
		{name: "validation", body: strings.Replace(validToolManifest("bad", "bad-package"), `"manifest_version": 1`, `"manifest_version": 99`, 1), wantError: "unsupported manifest version"},
		{name: "malformed", body: `{"manifest_version":`, wantError: "parse json"},
		{name: "unsupported runtime", body: strings.Replace(validToolManifest("bad", "bad-package"), `"runtime": "wasm"`, `"runtime": "native"`, 1), wantError: "unsupported runtime"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeToolPackage(t, root, "bad", tt.body)

			registry, result, err := tools.Discover(tools.DiscoverOptions{Roots: []string{root}})
			if err != nil {
				t.Fatalf("Discover: %v", err)
			}
			if registry.Count() != 0 || len(result.Registered) != 0 {
				t.Fatalf("registry/result = %d/%+v, want no registered tools", registry.Count(), result)
			}
			if len(result.Invalid) != 1 {
				t.Fatalf("invalid = %+v, want one invalid manifest", result.Invalid)
			}
			if !strings.Contains(result.Invalid[0].Error, tt.wantError) {
				t.Fatalf("invalid error = %q, want %q", result.Invalid[0].Error, tt.wantError)
			}
		})
	}
}

func TestDiscover_DuplicateToolNamesReportedDeterministically(t *testing.T) {
	root := t.TempDir()
	writeToolPackage(t, root, "first", validToolManifest("same_tool", "first-package"))
	writeToolPackage(t, root, "second", validToolManifest("same_tool", "second-package"))

	registry, result, err := tools.Discover(tools.DiscoverOptions{Roots: []string{root}})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if registry.Count() != 1 || len(result.Registered) != 1 {
		t.Fatalf("registered = %d/%+v, want first registered before duplicate", registry.Count(), result.Registered)
	}
	if len(result.Invalid) != 1 {
		t.Fatalf("invalid = %+v, want duplicate invalid", result.Invalid)
	}
	if !strings.Contains(result.Invalid[0].Error, tools.ErrDuplicateTool.Error()) {
		t.Fatalf("invalid error = %q, want duplicate tool error", result.Invalid[0].Error)
	}
}

func TestRegistry_LookupListDuplicateAndCloneBehavior(t *testing.T) {
	registry := tools.NewRegistry()
	entry := tools.Entry{
		Name:        "alpha",
		Description: "Alpha.",
		InputSchema: []byte(`{"type":"object"}`),
		Keywords:    []string{"one"},
		Permissions: manifest.DeclaredPermissions{
			Files: []manifest.FilePermission{{
				Path:  "notes/",
				Match: policy.MatchPrefix,
				Modes: []policy.FileAccessMode{policy.FileAccessRead},
			}},
		},
	}

	if err := registry.Register(entry); err != nil {
		t.Fatalf("Register: %v", err)
	}
	duplicateErr := registry.Register(entry)
	if !errors.Is(duplicateErr, tools.ErrDuplicateTool) {
		t.Fatalf("duplicate error = %v, want ErrDuplicateTool", duplicateErr)
	}

	found, ok := registry.Lookup("alpha")
	if !ok {
		t.Fatal("expected alpha lookup")
	}
	found.InputSchema[0] = '!'
	found.Keywords[0] = "changed"
	found.Permissions.Files[0].Modes[0] = policy.FileAccessWrite

	again, ok := registry.Lookup("alpha")
	if !ok {
		t.Fatal("expected alpha lookup after mutation")
	}
	if string(again.InputSchema) != `{"type":"object"}` || again.Keywords[0] != "one" || again.Permissions.Files[0].Modes[0] != policy.FileAccessRead {
		t.Fatalf("registry entry was not defensively cloned: %+v", again)
	}
	if list := registry.List(); len(list) != 1 || list[0].Name != "alpha" {
		t.Fatalf("list = %+v, want alpha", list)
	}
}

func TestDiscover_WasmPathRecordedWithoutExistenceValidation(t *testing.T) {
	root := t.TempDir()
	packageDir := writeToolPackage(t, root, "notes", validToolManifest("note_reader", "notes-tools"))

	registry, result, err := tools.Discover(tools.DiscoverOptions{Roots: []string{root}})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(result.Invalid) != 0 {
		t.Fatalf("invalid = %+v, want no invalid manifests despite missing wasm binary", result.Invalid)
	}
	entry, found := registry.Lookup("note_reader")
	if !found {
		t.Fatal("expected note_reader lookup")
	}
	if entry.WasmPath != filepath.Join(packageDir, tools.WasmFileName) {
		t.Fatalf("wasm path = %q, want convention path", entry.WasmPath)
	}
	if _, err := os.Stat(entry.WasmPath); !os.IsNotExist(err) {
		t.Fatalf("wasm binary exists or stat errored unexpectedly: %v", err)
	}
}

func TestDiscoverInto_RepeatedDiscoveryDoesNotDuplicateRegistryEntries(t *testing.T) {
	root := t.TempDir()
	writeToolPackage(t, root, "notes", validToolManifest("note_reader", "notes-tools"))
	registry := tools.NewRegistry()

	first, err := tools.DiscoverInto(registry, tools.DiscoverOptions{Roots: []string{root}})
	if err != nil {
		t.Fatalf("first DiscoverInto: %v", err)
	}
	if registry.Count() != 1 || len(first.Registered) != 1 || len(first.Invalid) != 0 {
		t.Fatalf("first discovery = count %d result %+v, want one registered tool", registry.Count(), first)
	}

	second, err := tools.DiscoverInto(registry, tools.DiscoverOptions{Roots: []string{root}})
	if err != nil {
		t.Fatalf("second DiscoverInto: %v", err)
	}
	if registry.Count() != 1 {
		t.Fatalf("registry count after repeated discovery = %d, want 1", registry.Count())
	}
	if len(second.Registered) != 0 || len(second.Invalid) != 1 {
		t.Fatalf("second discovery = %+v, want duplicate reported without extra registration", second)
	}
	if !strings.Contains(second.Invalid[0].Error, tools.ErrDuplicateTool.Error()) {
		t.Fatalf("second discovery error = %q, want duplicate tool error", second.Invalid[0].Error)
	}
}

func writeToolPackage(t *testing.T, root, packageName, body string) string {
	t.Helper()

	dir := filepath.Join(root, packageName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir tool package: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, tools.ManifestFileName), []byte(body), 0o600); err != nil {
		t.Fatalf("write tool manifest: %v", err)
	}
	return filepath.Clean(dir)
}

func validToolManifest(toolName, packageName string) string {
	return `{
		"manifest_version": 1,
		"name": "` + packageName + `",
		"description": "Useful workspace tools.",
		"runtime": "wasm",
		"tools": [
			{
				"name": "` + toolName + `",
				"description": "Read a note.",
				"input_schema": {"type": "object"}
			}
		],
		"permissions": {
			"files": [
				{"path": "notes/", "match": "prefix", "modes": ["read"]}
			]
		}
	}`
}

func manifestWithPermissions(permissions string) string {
	return `{
		"manifest_version": 1,
		"name": "perm-package",
		"description": "Permissioned tools.",
		"runtime": "wasm",
		"tools": [
			{
				"name": "perm_tool",
				"description": "Permissioned.",
				"input_schema": {"type": "object"}
			}
		],
		"permissions": {` + permissions + `}
	}`
}
