package tools_test

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/smasonuk/falken-core/internal/extensions/manifest"
	"github.com/smasonuk/falken-core/internal/extensions/tools"
	"github.com/smasonuk/falken-core/internal/policy"
)

func TestRuntimeRegistry_ActivateLookupAndIdempotent(t *testing.T) {
	registry := tools.NewRegistry()
	entry := activationEntry("alpha")
	if err := registry.Register(entry); err != nil {
		t.Fatalf("Register: %v", err)
	}

	runtime := tools.NewRuntimeRegistry(registry)
	if runtime.IsActive("alpha") {
		t.Fatal("alpha should start inactive")
	}
	if _, ok := runtime.Lookup("alpha"); ok {
		t.Fatal("inactive alpha should not be returned by active lookup")
	}

	activated, err := runtime.Activate("alpha")
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if activated.Name != "alpha" || !runtime.IsActive("alpha") || runtime.Count() != 1 {
		t.Fatalf("activated/count = %+v/%d, want active alpha count 1", activated, runtime.Count())
	}

	activated.InputSchema[0] = '!'
	activated.Keywords[0] = "changed"
	activated.Permissions.Files[0].Modes[0] = policy.FileAccessWrite

	again, err := runtime.Activate("alpha")
	if err != nil {
		t.Fatalf("Activate again: %v", err)
	}
	if runtime.Count() != 1 {
		t.Fatalf("count after idempotent activation = %d, want 1", runtime.Count())
	}
	if string(again.InputSchema) != `{"type":"object"}` || again.Keywords[0] != "workspace" || again.Permissions.Files[0].Modes[0] != policy.FileAccessRead {
		t.Fatalf("active metadata was not defensively cloned: %+v", again)
	}
}

func TestRuntimeRegistry_ActivateUnknownFails(t *testing.T) {
	runtime := tools.NewRuntimeRegistry(tools.NewRegistry())

	if _, err := runtime.Activate("missing"); !errors.Is(err, tools.ErrToolNotRegistered) {
		t.Fatalf("Activate missing error = %v, want ErrToolNotRegistered", err)
	}
	if err := runtime.Deactivate("missing"); !errors.Is(err, tools.ErrToolNotRegistered) {
		t.Fatalf("Deactivate missing error = %v, want ErrToolNotRegistered", err)
	}
}

func TestRuntimeRegistry_ListDeterministicAndExcludesInactive(t *testing.T) {
	registry := tools.NewRegistry()
	for _, name := range []string{"gamma", "alpha", "beta"} {
		if err := registry.Register(activationEntry(name)); err != nil {
			t.Fatalf("Register %s: %v", name, err)
		}
	}

	runtime := tools.NewRuntimeRegistry(registry)
	if _, err := runtime.Activate("gamma"); err != nil {
		t.Fatalf("Activate gamma: %v", err)
	}
	if _, err := runtime.Activate("alpha"); err != nil {
		t.Fatalf("Activate alpha: %v", err)
	}

	list := runtime.List()
	got := []string{list[0].Name, list[1].Name}
	if !reflect.DeepEqual(got, []string{"alpha", "gamma"}) {
		t.Fatalf("active list order = %v, want alpha,gamma", got)
	}
	if runtime.IsActive("beta") {
		t.Fatal("inactive registered beta should not be active")
	}
}

func TestRuntimeRegistry_MetadataPreserved(t *testing.T) {
	registry := tools.NewRegistry()
	entry := activationEntry("metadata")
	entry.Description = "Preserve this description."
	entry.Category = "analysis"
	entry.Keywords = []string{"workspace", "inspect"}
	entry.DefaultLoad = true
	entry.SourceDir = filepath.Join(t.TempDir(), "metadata")
	entry.ManifestPath = filepath.Join(entry.SourceDir, tools.ManifestFileName)
	entry.WasmPath = filepath.Join(entry.SourceDir, tools.WasmFileName)
	entry.Permissions = manifest.DeclaredPermissions{
		Files: []manifest.FilePermission{{
			Path:  "src/",
			Match: policy.MatchPrefix,
			Modes: []policy.FileAccessMode{policy.FileAccessRead, policy.FileAccessWrite},
		}},
		Network: []manifest.NetworkPermission{{
			Host:  "api.example.test",
			Match: policy.MatchExact,
		}},
		Shell: []manifest.ShellPermission{{
			Command: "go test",
			Match:   policy.MatchPrefix,
		}},
	}
	if err := registry.Register(entry); err != nil {
		t.Fatalf("Register: %v", err)
	}

	runtime := tools.NewRuntimeRegistry(registry)
	active, err := runtime.Activate("metadata")
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}

	if active.Description != entry.Description ||
		active.Category != entry.Category ||
		!reflect.DeepEqual(active.Keywords, entry.Keywords) ||
		active.PackageName != entry.PackageName ||
		active.PackageDesc != entry.PackageDesc ||
		active.SourceDir != entry.SourceDir ||
		active.ManifestPath != entry.ManifestPath ||
		active.WasmPath != entry.WasmPath ||
		active.Runtime != entry.Runtime ||
		active.ManifestVersion != entry.ManifestVersion ||
		!active.DefaultLoad {
		t.Fatalf("active metadata = %+v, want registered metadata preserved", active)
	}
	if !reflect.DeepEqual(active.Permissions, entry.Permissions) {
		t.Fatalf("active permissions = %+v, want %+v", active.Permissions, entry.Permissions)
	}
	if string(active.InputSchema) != string(entry.InputSchema) {
		t.Fatalf("active input schema = %s, want %s", active.InputSchema, entry.InputSchema)
	}
}

func TestRuntimeRegistry_ActivateDefaults(t *testing.T) {
	registry := tools.NewRegistry()
	always := activationEntry("always")
	always.AlwaysLoad = true
	defaulted := activationEntry("defaulted")
	defaulted.DefaultLoad = true
	manual := activationEntry("manual")
	for _, entry := range []tools.Entry{manual, defaulted, always} {
		if err := registry.Register(entry); err != nil {
			t.Fatalf("Register %s: %v", entry.Name, err)
		}
	}

	runtime := tools.NewRuntimeRegistry(registry)
	if err := runtime.ActivateDefaults(); err != nil {
		t.Fatalf("ActivateDefaults: %v", err)
	}

	if !runtime.IsActive("always") || !runtime.IsActive("defaulted") {
		t.Fatalf("default activation missing always/defaulted tools: %+v", runtime.List())
	}
	if runtime.IsActive("manual") {
		t.Fatal("manual tool should remain inactive after default activation")
	}
	list := runtime.List()
	got := []string{list[0].Name, list[1].Name}
	if !reflect.DeepEqual(got, []string{"always", "defaulted"}) {
		t.Fatalf("default active list = %v, want always,defaulted", got)
	}
}

func TestRuntimeRegistry_DeactivateBehavior(t *testing.T) {
	registry := tools.NewRegistry()
	normal := activationEntry("normal")
	always := activationEntry("always")
	always.AlwaysLoad = true
	inactive := activationEntry("inactive")
	for _, entry := range []tools.Entry{normal, always, inactive} {
		if err := registry.Register(entry); err != nil {
			t.Fatalf("Register %s: %v", entry.Name, err)
		}
	}

	runtime := tools.NewRuntimeRegistry(registry)
	if _, err := runtime.Activate("normal"); err != nil {
		t.Fatalf("Activate normal: %v", err)
	}
	if _, err := runtime.Activate("always"); err != nil {
		t.Fatalf("Activate always: %v", err)
	}

	if err := runtime.Deactivate("normal"); err != nil {
		t.Fatalf("Deactivate normal: %v", err)
	}
	if runtime.IsActive("normal") {
		t.Fatal("normal tool should be inactive after deactivation")
	}

	if err := runtime.Deactivate("inactive"); err != nil {
		t.Fatalf("Deactivate inactive registered tool: %v", err)
	}

	err := runtime.Deactivate("always")
	if !errors.Is(err, tools.ErrToolAlwaysLoad) {
		t.Fatalf("Deactivate always-load error = %v, want ErrToolAlwaysLoad", err)
	}
	if !runtime.IsActive("always") {
		t.Fatal("always-load tool should remain active after failed deactivation")
	}
}

func TestRuntimeRegistry_DoesNotRequireWasmBinary(t *testing.T) {
	registry := tools.NewRegistry()
	entry := activationEntry("missing_wasm")
	entry.WasmPath = filepath.Join(t.TempDir(), "does-not-exist.wasm")
	if err := registry.Register(entry); err != nil {
		t.Fatalf("Register: %v", err)
	}

	runtime := tools.NewRuntimeRegistry(registry)
	active, err := runtime.Activate("missing_wasm")
	if err != nil {
		t.Fatalf("Activate with missing wasm binary: %v", err)
	}
	if active.WasmPath != entry.WasmPath {
		t.Fatalf("active wasm path = %q, want metadata path %q", active.WasmPath, entry.WasmPath)
	}
}

func activationEntry(name string) tools.Entry {
	return tools.Entry{
		Name:            name,
		Description:     "Tool " + name + ".",
		InputSchema:     []byte(`{"type":"object"}`),
		Category:        "workspace",
		Keywords:        []string{"workspace"},
		PackageName:     "package-" + name,
		PackageDesc:     "Package " + name + ".",
		SourceDir:       "/tmp/tools/" + name,
		ManifestPath:    "/tmp/tools/" + name + "/" + tools.ManifestFileName,
		WasmPath:        "/tmp/tools/" + name + "/" + tools.WasmFileName,
		Runtime:         manifest.RuntimeWasm,
		ManifestVersion: manifest.SupportedManifestVersion,
		Permissions: manifest.DeclaredPermissions{
			Files: []manifest.FilePermission{{
				Path:  name + "/",
				Match: policy.MatchPrefix,
				Modes: []policy.FileAccessMode{policy.FileAccessRead},
			}},
		},
	}
}
