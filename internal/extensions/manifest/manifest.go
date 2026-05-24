package manifest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/smasonuk/falken-core/internal/policy"
	"github.com/smasonuk/falken-core/internal/toolvalidation"
)

const (
	// SupportedManifestVersion is the only extension manifest schema version supported by v1.
	SupportedManifestVersion = 1
	// RuntimeWasm is the only supported extension runtime in v1.
	RuntimeWasm RuntimeKind = "wasm"
)

var (
	// ErrInvalidManifest indicates manifest parsing or validation failed deterministically.
	ErrInvalidManifest = errors.New("invalid extension manifest")
)

// RuntimeKind identifies the runtime used by an extension package.
type RuntimeKind string

// HookEvent identifies a plugin lifecycle/runtime event.
type HookEvent string

const (
	HookEventSessionStart       HookEvent = "session_start"
	HookEventSessionClose       HookEvent = "session_close"
	HookEventBeforeCommand      HookEvent = "before_command"
	HookEventAfterCommand       HookEvent = "after_command"
	HookEventBeforeFileMutation HookEvent = "before_file_mutation"
	HookEventAfterFileMutation  HookEvent = "after_file_mutation"
)

// ToolManifest is the versioned manifest model for Wasm tool packages.
type ToolManifest struct {
	ManifestVersion int                 `json:"manifest_version"`
	Name            string              `json:"name"`
	Description     string              `json:"description"`
	Runtime         RuntimeKind         `json:"runtime"`
	Tools           []ToolDefinition    `json:"tools"`
	Permissions     DeclaredPermissions `json:"permissions,omitempty"`
}

// PluginManifest is the versioned manifest model for Wasm plugin packages.
type PluginManifest struct {
	ManifestVersion int                 `json:"manifest_version"`
	Name            string              `json:"name"`
	Description     string              `json:"description"`
	Runtime         RuntimeKind         `json:"runtime"`
	Hooks           []HookDefinition    `json:"hooks"`
	Permissions     DeclaredPermissions `json:"permissions,omitempty"`
}

// ToolDefinition declares one callable tool.
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
	Category    string          `json:"category,omitempty"`
	Keywords    []string        `json:"keywords,omitempty"`
	AlwaysLoad  bool            `json:"always_load,omitempty"`
	DefaultLoad bool            `json:"default_load,omitempty"`
}

// HookDefinition declares one plugin hook.
type HookDefinition struct {
	Name  string    `json:"name"`
	Event HookEvent `json:"event"`
}

// DeclaredPermissions captures runtime capabilities requested by an extension.
type DeclaredPermissions struct {
	Files   []FilePermission    `json:"files,omitempty"`
	Network []NetworkPermission `json:"network,omitempty"`
	Shell   []ShellPermission   `json:"shell,omitempty"`
}

// FilePermission declares file access requested by an extension.
type FilePermission struct {
	Path  string                  `json:"path"`
	Match policy.MatchKind        `json:"match"`
	Modes []policy.FileAccessMode `json:"modes,omitempty"`
}

// NetworkPermission declares network access requested by an extension.
type NetworkPermission struct {
	Host  string           `json:"host"`
	Match policy.MatchKind `json:"match"`
}

// ShellPermission declares shell access requested by an extension.
type ShellPermission struct {
	Command string           `json:"command"`
	Match   policy.MatchKind `json:"match"`
}

// ValidationError identifies one invalid manifest field.
type ValidationError struct {
	Field  string
	Reason string
}

func (e ValidationError) Error() string {
	return ErrInvalidManifest.Error() + ": " + e.Field + ": " + e.Reason
}

// Is lets callers use errors.Is(err, ErrInvalidManifest) with validation failures.
func (e ValidationError) Is(target error) bool {
	return target == ErrInvalidManifest
}

// ParseToolManifest decodes strict JSON and validates a tool manifest.
func ParseToolManifest(data []byte) (ToolManifest, error) {
	var manifest ToolManifest
	if err := decodeStrictJSON(data, &manifest); err != nil {
		return ToolManifest{}, err
	}
	if err := manifest.Validate(); err != nil {
		return ToolManifest{}, err
	}
	return manifest, nil
}

// ParseToolManifestString decodes strict JSON and validates a tool manifest from a string.
func ParseToolManifestString(data string) (ToolManifest, error) {
	return ParseToolManifest([]byte(data))
}

// ParsePluginManifest decodes strict JSON and validates a plugin manifest.
func ParsePluginManifest(data []byte) (PluginManifest, error) {
	var manifest PluginManifest
	if err := decodeStrictJSON(data, &manifest); err != nil {
		return PluginManifest{}, err
	}
	if err := manifest.Validate(); err != nil {
		return PluginManifest{}, err
	}
	return manifest, nil
}

// ParsePluginManifestString decodes strict JSON and validates a plugin manifest from a string.
func ParsePluginManifestString(data string) (PluginManifest, error) {
	return ParsePluginManifest([]byte(data))
}

// Validate checks a tool manifest without depending on runtime/session state.
func (m ToolManifest) Validate() error {
	if err := validateCommon(m.ManifestVersion, m.Name, m.Description, m.Runtime); err != nil {
		return err
	}
	if len(m.Tools) == 0 {
		return validationError("tools", "at least one tool is required")
	}

	seen := make(map[string]struct{}, len(m.Tools))
	for i, tool := range m.Tools {
		prefix := "tools[" + strconv.Itoa(i) + "]"
		if err := validateName(prefix+".name", tool.Name); err != nil {
			return err
		}
		if err := validateDescription(prefix+".description", tool.Description); err != nil {
			return err
		}
		if err := validateInputSchema(prefix+".input_schema", tool.InputSchema); err != nil {
			return err
		}
		if _, ok := seen[tool.Name]; ok {
			return validationError(prefix+".name", "duplicate tool name "+tool.Name)
		}
		seen[tool.Name] = struct{}{}
	}

	return validateDeclaredPermissions(m.Permissions)
}

// Validate checks a plugin manifest without depending on runtime/session state.
func (m PluginManifest) Validate() error {
	if err := validateCommon(m.ManifestVersion, m.Name, m.Description, m.Runtime); err != nil {
		return err
	}
	if len(m.Hooks) == 0 {
		return validationError("hooks", "at least one hook is required")
	}

	seen := make(map[string]struct{}, len(m.Hooks))
	for i, hook := range m.Hooks {
		prefix := "hooks[" + strconv.Itoa(i) + "]"
		if err := validateName(prefix+".name", hook.Name); err != nil {
			return err
		}
		if !isSupportedHookEvent(hook.Event) {
			return validationError(prefix+".event", "unsupported or missing hook event")
		}
		if _, ok := seen[hook.Name]; ok {
			return validationError(prefix+".name", "duplicate hook name "+hook.Name)
		}
		seen[hook.Name] = struct{}{}
	}

	return validateDeclaredPermissions(m.Permissions)
}

func decodeStrictJSON[T any](data []byte, target *T) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%w: parse json: %w", ErrInvalidManifest, err)
	}
	var extra any
	err := decoder.Decode(&extra)
	if err == nil {
		return fmt.Errorf("%w: parse json: multiple json values are not supported", ErrInvalidManifest)
	}
	if !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: parse json: multiple json values are not supported", ErrInvalidManifest)
	}
	return nil
}

func validateCommon(version int, name, description string, runtime RuntimeKind) error {
	if version == 0 {
		return validationError("manifest_version", "manifest version is required")
	}
	if version != SupportedManifestVersion {
		return validationError("manifest_version", "unsupported manifest version")
	}
	if err := validateName("name", name); err != nil {
		return err
	}
	if err := validateDescription("description", description); err != nil {
		return err
	}
	if runtime == "" {
		return validationError("runtime", "runtime is required")
	}
	if runtime != RuntimeWasm {
		return validationError("runtime", "unsupported runtime "+string(runtime))
	}
	return nil
}

func validateName(field, name string) error {
	if err := toolvalidation.ValidateName(name); err != nil {
		return validationError(field, err.Error())
	}
	return nil
}

func validateDescription(field, description string) error {
	if err := toolvalidation.ValidateDescription(description); err != nil {
		return validationError(field, err.Error())
	}
	return nil
}

func validateInputSchema(field string, schema json.RawMessage) error {
	if err := toolvalidation.ValidateObjectSchema(schema); err != nil {
		if errors.Is(err, toolvalidation.ErrSchemaPropertiesNotObject) {
			return validationError(field+".properties", "properties must be a json object")
		}
		if strings.Contains(err.Error(), "required") {
			return validationError(field, "input parameter schema is required")
		}
		if strings.Contains(err.Error(), "valid JSON") {
			return validationError(field, "input parameter schema must be a json object")
		}
		return validationError(field, "input parameter schema must be a json object schema")
	}
	return nil
}

func validateDeclaredPermissions(permissions DeclaredPermissions) error {
	for i, permission := range permissions.Files {
		prefix := "permissions.files[" + strconv.Itoa(i) + "]"
		if strings.TrimSpace(permission.Path) == "" {
			return validationError(prefix+".path", "file path is required")
		}
		if !isSupportedMatchKind(permission.Match) {
			return validationError(prefix+".match", "unsupported or missing match kind")
		}
		for j, mode := range permission.Modes {
			if !isSupportedFileAccessMode(mode) {
				return validationError(prefix+".modes["+strconv.Itoa(j)+"]", "unsupported file access mode")
			}
		}
	}

	for i, permission := range permissions.Network {
		prefix := "permissions.network[" + strconv.Itoa(i) + "]"
		if strings.TrimSpace(permission.Host) == "" {
			return validationError(prefix+".host", "network host is required")
		}
		if !isSupportedMatchKind(permission.Match) {
			return validationError(prefix+".match", "unsupported or missing match kind")
		}
	}

	for i, permission := range permissions.Shell {
		prefix := "permissions.shell[" + strconv.Itoa(i) + "]"
		if strings.TrimSpace(permission.Command) == "" {
			return validationError(prefix+".command", "shell command is required")
		}
		if !isSupportedMatchKind(permission.Match) {
			return validationError(prefix+".match", "unsupported or missing match kind")
		}
	}

	return nil
}

func isSupportedMatchKind(match policy.MatchKind) bool {
	switch match {
	case policy.MatchExact, policy.MatchPrefix, policy.MatchSuffix:
		return true
	default:
		return false
	}
}

func isSupportedFileAccessMode(mode policy.FileAccessMode) bool {
	switch mode {
	case policy.FileAccessRead, policy.FileAccessWrite, policy.FileAccessCreate:
		return true
	default:
		return false
	}
}

func isSupportedHookEvent(event HookEvent) bool {
	switch event {
	case HookEventSessionStart,
		HookEventSessionClose,
		HookEventBeforeCommand,
		HookEventAfterCommand,
		HookEventBeforeFileMutation,
		HookEventAfterFileMutation:
		return true
	default:
		return false
	}
}

func validationError(field, reason string) error {
	return ValidationError{Field: field, Reason: reason}
}
