package permissions

import (
	"os"

	"gopkg.in/yaml.v3"
)

var DefaultBlockedFiles = []string{
	// Standard Secrets
	".env",
	".env.*",
	"*.pem",
	"*.key",
	"id_rsa",
	"id_rsa.pub",
	"secrets.json",
	"credentials.json",
	"*.p12",

	// Git Security (Prevent malicious remote/hook injection)
	".git/config",
	".git/hooks/*",

	// Falken Internal Security
	".falken.yaml",           // Prevent agent from elevating its own permissions
	".falken/history*.jsonl", // Prevent agent from reading/corrupting its raw memory
	".falken/debug.log",
	".falken/cache/*", // Protect the proxy cert and blackhole
}

type CacheConfig struct {
	ContainerPath string   `yaml:"container_path"`
	Env           []string `yaml:"env"`
}

type Config struct {
	SandboxImage           string `yaml:"sandbox_image"`
	ShowPermissionOverview bool   `yaml:"show_permission_overview"` // Default true

	// Caches defines project-local cache mounts and environment variables
	Caches map[string]CacheConfig `yaml:"caches"`

	// Global allow/block lists are explicit config authored by the user or setup flow.
	// They only become strict allowlists when the matching Strict*Allowlist toggle is enabled.
	GlobalBlockedURLs     []string `yaml:"global_blocked_urls"`
	GlobalAllowedURLs     []string `yaml:"global_allowed_urls"`
	GlobalBlockedFiles    []string `yaml:"global_blocked_files"`
	GlobalAllowedFiles    []string `yaml:"global_allowed_files"`
	GlobalBlockedCommands []string `yaml:"global_blocked_commands"`
	GlobalAllowedCommands []string `yaml:"global_allowed_commands"`

	// Persistent approvals come from interactive "allow permanently" decisions.
	PersistentAllowedURLs     []string          `yaml:"persistent_allowed_urls"`
	PersistentAllowedCommands []string          `yaml:"persistent_allowed_commands"`
	PersistentAllowedFiles    map[string]string `yaml:"persistent_allowed_files"`

	// When enabled, unmatched file/shell accesses are denied instead of prompting.
	StrictFileAllowlist    bool `yaml:"strict_file_allowlist"`
	StrictCommandAllowlist bool `yaml:"strict_command_allowlist"`

	ApprovedPlugins map[string]bool `yaml:"approved_plugins"` // Tracks AOT plugin approvals
}

type legacyConfig struct {
	ProjectDotfiles map[string]string `yaml:"project_dotfiles"`
}

func LoadConfig() (*Config, error) {
	return LoadConfigFromPath(".falken.yaml")
}

func LoadConfigFromPath(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			cfg := &Config{
				SandboxImage:           "falken/sandbox:latest",
				ShowPermissionOverview: true,
				ApprovedPlugins:        make(map[string]bool),
				Caches:                 make(map[string]CacheConfig),
			}
			cfg.ensureDefaults()
			return cfg, nil
		}
		return nil, err
	}

	var config Config
	var legacy legacyConfig
	// Set default before unmarshal if it's a new field
	config.ShowPermissionOverview = true

	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(data, &legacy); err != nil {
		return nil, err
	}

	config.ensureDefaults()
	if len(config.PersistentAllowedFiles) == 0 && len(legacy.ProjectDotfiles) > 0 {
		config.PersistentAllowedFiles = legacy.ProjectDotfiles
	}

	return &config, nil
}

func SaveConfig(config *Config) error {
	return SaveConfigToPath(".falken.yaml", config)
}

func SaveConfigToPath(path string, config *Config) error {
	if config != nil {
		config.ensureDefaults()
	}
	data, err := yaml.Marshal(config)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func (c *Config) ensureDefaults() {
	if c == nil {
		return
	}
	if c.SandboxImage == "" {
		c.SandboxImage = "falken/sandbox:latest"
	}
	if c.ApprovedPlugins == nil {
		c.ApprovedPlugins = make(map[string]bool)
	}
	if c.Caches == nil {
		c.Caches = make(map[string]CacheConfig)
	}
	if c.PersistentAllowedFiles == nil {
		c.PersistentAllowedFiles = make(map[string]string)
	}
}

func (c *Config) AddGlobalAllowedURL(target string) bool {
	return appendUniqueString(&c.GlobalAllowedURLs, target)
}

func (c *Config) AddPersistentAllowedURL(target string) bool {
	return appendUniqueString(&c.PersistentAllowedURLs, target)
}

func (c *Config) AddPersistentAllowedCommand(target string) bool {
	return appendUniqueString(&c.PersistentAllowedCommands, target)
}

func (c *Config) SetPersistentAllowedFile(path, access string) bool {
	if c.PersistentAllowedFiles == nil {
		c.PersistentAllowedFiles = make(map[string]string)
	}
	current, ok := c.PersistentAllowedFiles[path]
	if !ok || fileAccessRank(access) > fileAccessRank(current) {
		c.PersistentAllowedFiles[path] = access
		return true
	}
	return false
}

func appendUniqueString(dst *[]string, value string) bool {
	if value == "" {
		return false
	}
	for _, existing := range *dst {
		if existing == value {
			return false
		}
	}
	*dst = append(*dst, value)
	return true
}

func fileAccessRank(access string) int {
	switch access {
	case "read/write":
		return 2
	case "read":
		return 1
	default:
		return 0
	}
}
