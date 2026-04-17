package manifest

import "fmt"

type FileAccess struct {
	Path   string `yaml:"path"`
	Access string `yaml:"access"` // "read" or "read-write"
}

type NetworkRule struct {
	Domain string `yaml:"domain,omitempty"`
	URL    string `yaml:"url,omitempty"`
}

type GranularPermissions struct {
	Files   []FileAccess  `yaml:"files"`
	Network []NetworkRule `yaml:"network"`
	Shell   []string      `yaml:"shell"`
}

type ToolDefinition struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	Parameters  map[string]any `yaml:"parameters"`
	AlwaysLoad  bool           `yaml:"always_load"`
	Keywords    []string       `yaml:"keywords"`
	Category    string         `yaml:"category"`
}

type HookDefinition struct {
	Name  string `yaml:"name"`
	Event string `yaml:"event"` // e.g., "on_startup"
}

type ToolManifest struct {
	Name                 string              `yaml:"name"`
	Description          string              `yaml:"description"`
	Tools                []ToolDefinition    `yaml:"tools"`
	RequestedPermissions GranularPermissions `yaml:"requested_permissions"`
}

type PluginManifest struct {
	Name        string              `yaml:"name"`
	Description string              `yaml:"description"`
	Hooks       []HookDefinition    `yaml:"hooks"`
	Permissions GranularPermissions `yaml:"permissions"`
}

func (p GranularPermissions) Validate() error {
	for i, file := range p.Files {
		if file.Path == "" {
			return fmt.Errorf("permissions.files[%d].path is required", i)
		}
		if file.Access != "read" && file.Access != "read-write" {
			return fmt.Errorf("permissions.files[%d].access must be 'read' or 'read-write'", i)
		}
	}
	for i, rule := range p.Network {
		hasDomain := rule.Domain != ""
		hasURL := rule.URL != ""
		if hasDomain == hasURL {
			return fmt.Errorf("permissions.network[%d] must set exactly one of domain or url", i)
		}
	}
	for i, cmd := range p.Shell {
		if cmd == "" {
			return fmt.Errorf("permissions.shell[%d] must not be empty", i)
		}
	}
	return nil
}

func (t ToolDefinition) Validate() error {
	if t.Name == "" {
		return fmt.Errorf("tool name is required")
	}
	if t.Description == "" {
		return fmt.Errorf("tool %q description is required", t.Name)
	}
	if t.Parameters == nil {
		return fmt.Errorf("tool %q parameters are required", t.Name)
	}
	return nil
}

func (h HookDefinition) Validate() error {
	if h.Name == "" {
		return fmt.Errorf("hook name is required")
	}
	if h.Event == "" {
		return fmt.Errorf("hook %q event is required", h.Name)
	}
	return nil
}

func (m ToolManifest) Validate() error {
	if m.Name == "" {
		return fmt.Errorf("name is required")
	}
	if m.Description == "" {
		return fmt.Errorf("description is required")
	}
	if len(m.Tools) == 0 {
		return fmt.Errorf("tools must not be empty")
	}
	if err := m.RequestedPermissions.Validate(); err != nil {
		return fmt.Errorf("requested_permissions: %w", err)
	}
	for i, tool := range m.Tools {
		if err := tool.Validate(); err != nil {
			return fmt.Errorf("tools[%d]: %w", i, err)
		}
	}
	return nil
}

func (m PluginManifest) Validate() error {
	if m.Name == "" {
		return fmt.Errorf("name is required")
	}
	if m.Description == "" {
		return fmt.Errorf("description is required")
	}
	if len(m.Hooks) == 0 {
		return fmt.Errorf("hooks must not be empty")
	}
	if err := m.Permissions.Validate(); err != nil {
		return fmt.Errorf("permissions: %w", err)
	}
	for i, hook := range m.Hooks {
		if err := hook.Validate(); err != nil {
			return fmt.Errorf("hooks[%d]: %w", i, err)
		}
	}
	return nil
}
