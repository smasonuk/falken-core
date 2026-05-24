package manifest_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/smasonuk/falken-core/internal/extensions/manifest"
)

func TestParseToolManifest_ValidMinimal(t *testing.T) {
	parsed, err := manifest.ParseToolManifestString(validToolManifest())
	if err != nil {
		t.Fatalf("ParseToolManifestString: %v", err)
	}

	if parsed.ManifestVersion != manifest.SupportedManifestVersion {
		t.Fatalf("version = %d, want %d", parsed.ManifestVersion, manifest.SupportedManifestVersion)
	}
	if parsed.Runtime != manifest.RuntimeWasm {
		t.Fatalf("runtime = %q, want %q", parsed.Runtime, manifest.RuntimeWasm)
	}
	if parsed.Name != "core-tools" || len(parsed.Tools) != 1 {
		t.Fatalf("parsed manifest = %+v", parsed)
	}
}

func TestParseToolManifest_ValidWithPermissionsAndMultipleTools(t *testing.T) {
	parsed, err := manifest.ParseToolManifestString(`{
		"manifest_version": 1,
		"name": "core-tools",
		"description": "Useful workspace tools.",
		"runtime": "wasm",
		"tools": [
			{
				"name": "read_note",
				"description": "Read a note.",
				"input_schema": {"type": "object"},
				"category": "files",
				"keywords": ["read", "note"],
				"always_load": true
			},
			{
				"name": "write_note",
				"description": "Write a note.",
				"input_schema": {"type": "object"},
				"default_load": true
			}
		],
		"permissions": {
			"files": [
				{"path": "notes/", "match": "prefix", "modes": ["read", "write", "create"]}
			],
			"network": [
				{"host": "api.example.com", "match": "exact"}
			],
			"shell": [
				{"command": "git status", "match": "exact"}
			]
		}
	}`)
	if err != nil {
		t.Fatalf("ParseToolManifestString: %v", err)
	}

	if len(parsed.Tools) != 2 {
		t.Fatalf("tool count = %d, want 2", len(parsed.Tools))
	}
	if len(parsed.Permissions.Files) != 1 || len(parsed.Permissions.Network) != 1 || len(parsed.Permissions.Shell) != 1 {
		t.Fatalf("permissions = %+v, want file/network/shell declarations", parsed.Permissions)
	}
}

func TestParseToolManifest_InvalidManifests(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantField string
		wantText  string
	}{
		{
			name:      "missing version",
			body:      replaceToolManifest(`"manifest_version": 1,`, ""),
			wantField: "manifest_version",
			wantText:  "required",
		},
		{
			name:      "unsupported version",
			body:      replaceToolManifest(`"manifest_version": 1`, `"manifest_version": 99`),
			wantField: "manifest_version",
			wantText:  "unsupported",
		},
		{
			name:      "missing name",
			body:      replaceToolManifest(`"name": "core-tools",`, ""),
			wantField: "name",
			wantText:  "required",
		},
		{
			name:      "invalid name",
			body:      replaceToolManifest(`"name": "core-tools"`, `"name": "123 bad"`),
			wantField: "name",
			wantText:  "must start",
		},
		{
			name:      "missing description",
			body:      replaceToolManifest(`"description": "Useful workspace tools.",`, ""),
			wantField: "description",
			wantText:  "required",
		},
		{
			name:      "unsupported runtime",
			body:      replaceToolManifest(`"runtime": "wasm"`, `"runtime": "native"`),
			wantField: "runtime",
			wantText:  "unsupported runtime",
		},
		{
			name: "no tools",
			body: `{
				"manifest_version": 1,
				"name": "core-tools",
				"description": "Useful workspace tools.",
				"runtime": "wasm",
				"tools": []
			}`,
			wantField: "tools",
			wantText:  "at least one",
		},
		{
			name:      "tool missing name",
			body:      replaceToolManifest(`"name": "read_note",`, ""),
			wantField: "tools[0].name",
			wantText:  "required",
		},
		{
			name:      "tool missing description",
			body:      replaceToolManifest(`"description": "Read a note.",`, ""),
			wantField: "tools[0].description",
			wantText:  "required",
		},
		{
			name:      "tool missing input schema",
			body:      replaceToolManifest(`"input_schema": {"type": "object"}`, `"category": "files"`),
			wantField: "tools[0].input_schema",
			wantText:  "required",
		},
		{
			name:      "tool null input schema",
			body:      replaceToolManifest(`"input_schema": {"type": "object"}`, `"input_schema": null`),
			wantField: "tools[0].input_schema",
			wantText:  "object schema",
		},
		{
			name:      "tool non-object input schema",
			body:      replaceToolManifest(`"input_schema": {"type": "object"}`, `"input_schema": {"type": "string"}`),
			wantField: "tools[0].input_schema",
			wantText:  "object schema",
		},
		{
			name:      "tool non-object properties",
			body:      replaceToolManifest(`"input_schema": {"type": "object"}`, `"input_schema": {"type": "object", "properties": []}`),
			wantField: "tools[0].input_schema.properties",
			wantText:  "properties",
		},
		{
			name: "duplicate tools",
			body: `{
				"manifest_version": 1,
				"name": "core-tools",
				"description": "Useful workspace tools.",
				"runtime": "wasm",
				"tools": [
					{"name": "same", "description": "First.", "input_schema": {"type": "object"}},
					{"name": "same", "description": "Second.", "input_schema": {"type": "object"}}
				]
			}`,
			wantField: "tools[1].name",
			wantText:  "duplicate",
		},
		{
			name:      "invalid file permission mode",
			body:      replaceToolManifest(`"permissions": {}`, `"permissions": {"files": [{"path": "notes/", "match": "prefix", "modes": ["delete"]}]}`),
			wantField: "permissions.files[0].modes[0]",
			wantText:  "unsupported",
		},
		{
			name:      "invalid network permission",
			body:      replaceToolManifest(`"permissions": {}`, `"permissions": {"network": [{"host": "", "match": "exact"}]}`),
			wantField: "permissions.network[0].host",
			wantText:  "required",
		},
		{
			name:      "invalid shell permission",
			body:      replaceToolManifest(`"permissions": {}`, `"permissions": {"shell": [{"command": "go test", "match": "glob"}]}`),
			wantField: "permissions.shell[0].match",
			wantText:  "unsupported",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := manifest.ParseToolManifestString(tt.body)
			assertManifestError(t, err, tt.wantField, tt.wantText)
		})
	}
}

func TestParsePluginManifest_ValidMinimal(t *testing.T) {
	parsed, err := manifest.ParsePluginManifestString(validPluginManifest())
	if err != nil {
		t.Fatalf("ParsePluginManifestString: %v", err)
	}

	if parsed.Name != "session-plugin" || parsed.Runtime != manifest.RuntimeWasm || len(parsed.Hooks) != 1 {
		t.Fatalf("parsed manifest = %+v", parsed)
	}
}

func TestParsePluginManifest_ValidWithPermissionsAndMultipleHooks(t *testing.T) {
	parsed, err := manifest.ParsePluginManifestString(`{
		"manifest_version": 1,
		"name": "session-plugin",
		"description": "Session lifecycle plugin.",
		"runtime": "wasm",
		"hooks": [
			{"name": "on_start", "event": "session_start"},
			{"name": "after_command", "event": "after_command"}
		],
		"permissions": {
			"files": [{"path": "notes/", "match": "prefix", "modes": ["read"]}],
			"network": [{"host": "api.example.com", "match": "suffix"}],
			"shell": [{"command": "git", "match": "prefix"}]
		}
	}`)
	if err != nil {
		t.Fatalf("ParsePluginManifestString: %v", err)
	}

	if len(parsed.Hooks) != 2 {
		t.Fatalf("hook count = %d, want 2", len(parsed.Hooks))
	}
	if len(parsed.Permissions.Files) != 1 || len(parsed.Permissions.Network) != 1 || len(parsed.Permissions.Shell) != 1 {
		t.Fatalf("permissions = %+v, want file/network/shell declarations", parsed.Permissions)
	}
}

func TestParsePluginManifest_InvalidManifests(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantField string
		wantText  string
	}{
		{
			name:      "missing version",
			body:      replacePluginManifest(`"manifest_version": 1,`, ""),
			wantField: "manifest_version",
			wantText:  "required",
		},
		{
			name:      "unsupported version",
			body:      replacePluginManifest(`"manifest_version": 1`, `"manifest_version": 2`),
			wantField: "manifest_version",
			wantText:  "unsupported",
		},
		{
			name:      "missing name",
			body:      replacePluginManifest(`"name": "session-plugin",`, ""),
			wantField: "name",
			wantText:  "required",
		},
		{
			name:      "missing description",
			body:      replacePluginManifest(`"description": "Session lifecycle plugin.",`, ""),
			wantField: "description",
			wantText:  "required",
		},
		{
			name: "no hooks",
			body: `{
				"manifest_version": 1,
				"name": "session-plugin",
				"description": "Session lifecycle plugin.",
				"runtime": "wasm",
				"hooks": []
			}`,
			wantField: "hooks",
			wantText:  "at least one",
		},
		{
			name:      "hook missing name",
			body:      replacePluginManifest(`"name": "on_start",`, ""),
			wantField: "hooks[0].name",
			wantText:  "required",
		},
		{
			name:      "hook missing event",
			body:      replacePluginManifest(`"event": "session_start"`, `"event": ""`),
			wantField: "hooks[0].event",
			wantText:  "unsupported",
		},
		{
			name: "duplicate hooks",
			body: `{
				"manifest_version": 1,
				"name": "session-plugin",
				"description": "Session lifecycle plugin.",
				"runtime": "wasm",
				"hooks": [
					{"name": "same", "event": "session_start"},
					{"name": "same", "event": "session_close"}
				]
			}`,
			wantField: "hooks[1].name",
			wantText:  "duplicate",
		},
		{
			name:      "invalid permission",
			body:      replacePluginManifest(`"permissions": {}`, `"permissions": {"files": [{"path": "", "match": "exact"}]}`),
			wantField: "permissions.files[0].path",
			wantText:  "required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := manifest.ParsePluginManifestString(tt.body)
			assertManifestError(t, err, tt.wantField, tt.wantText)
		})
	}
}

func TestParseManifest_StrictParseErrors(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "malformed", body: `{"manifest_version":`, want: "parse json"},
		{name: "tool unknown field", body: replaceToolManifest(`"permissions": {}`, `"permissions": {}, "unexpected": true`), want: "unknown field"},
		{name: "plugin unknown field", body: replacePluginManifest(`"permissions": {}`, `"permissions": {}, "unexpected": true`), want: "unknown field"},
		{name: "tool multiple values", body: validToolManifest() + `{}`, want: "multiple json values"},
		{name: "plugin multiple values", body: validPluginManifest() + `{}`, want: "multiple json values"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseManifestByName(tt.name, tt.body)
			if err == nil {
				t.Fatal("expected parse error")
			}
			if !errors.Is(err, manifest.ErrInvalidManifest) {
				t.Fatalf("error = %v, want ErrInvalidManifest", err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want to contain %q", err.Error(), tt.want)
			}
		})
	}
}

func parseManifestByName(name, body string) (any, error) {
	if strings.Contains(name, "plugin") {
		return manifest.ParsePluginManifestString(body)
	}
	return manifest.ParseToolManifestString(body)
}

func validToolManifest() string {
	return `{
		"manifest_version": 1,
		"name": "core-tools",
		"description": "Useful workspace tools.",
		"runtime": "wasm",
		"tools": [
			{
				"name": "read_note",
				"description": "Read a note.",
				"input_schema": {"type": "object"}
			}
		],
		"permissions": {}
	}`
}

func validPluginManifest() string {
	return `{
		"manifest_version": 1,
		"name": "session-plugin",
		"description": "Session lifecycle plugin.",
		"runtime": "wasm",
		"hooks": [
			{"name": "on_start", "event": "session_start"}
		],
		"permissions": {}
	}`
}

func replaceToolManifest(oldValue, newValue string) string {
	return strings.Replace(validToolManifest(), oldValue, newValue, 1)
}

func replacePluginManifest(oldValue, newValue string) string {
	return strings.Replace(validPluginManifest(), oldValue, newValue, 1)
}

func assertManifestError(t *testing.T, err error, field, text string) {
	t.Helper()

	if err == nil {
		t.Fatal("expected manifest error")
	}
	if !errors.Is(err, manifest.ErrInvalidManifest) {
		t.Fatalf("error = %v, want ErrInvalidManifest", err)
	}
	if !strings.Contains(err.Error(), field) {
		t.Fatalf("error = %q, want field %q", err.Error(), field)
	}
	if !strings.Contains(err.Error(), text) {
		t.Fatalf("error = %q, want text %q", err.Error(), text)
	}
}
