package falken

import "github.com/smasonuk/falken-core/internal/permissions"

type CacheConfig struct {
	ContainerPath string   `yaml:"container_path"`
	Env           []string `yaml:"env"`
}

type PermissionsConfig struct {
	SandboxImage              string                 `yaml:"sandbox_image"`
	ShowPermissionOverview    bool                   `yaml:"show_permission_overview"`
	Caches                    map[string]CacheConfig `yaml:"caches"`
	GlobalBlockedURLs         []string               `yaml:"global_blocked_urls"`
	GlobalAllowedURLs         []string               `yaml:"global_allowed_urls"`
	GlobalBlockedFiles        []string               `yaml:"global_blocked_files"`
	GlobalAllowedFiles        []string               `yaml:"global_allowed_files"`
	GlobalBlockedCommands     []string               `yaml:"global_blocked_commands"`
	GlobalAllowedCommands     []string               `yaml:"global_allowed_commands"`
	PersistentAllowedURLs     []string               `yaml:"persistent_allowed_urls"`
	PersistentAllowedCommands []string               `yaml:"persistent_allowed_commands"`
	PersistentAllowedFiles    map[string]string      `yaml:"persistent_allowed_files"`
	StrictFileAllowlist       bool                   `yaml:"strict_file_allowlist"`
	StrictCommandAllowlist    bool                   `yaml:"strict_command_allowlist"`
	ApprovedPlugins           map[string]bool        `yaml:"approved_plugins"`
}

func LoadPermissionsConfig() (*PermissionsConfig, error) {
	cfg, err := permissions.LoadConfig()
	if err != nil {
		return nil, err
	}
	return fromInternalPermissionsConfig(cfg), nil
}

func LoadPermissionsConfigFromPath(path string) (*PermissionsConfig, error) {
	cfg, err := permissions.LoadConfigFromPath(path)
	if err != nil {
		return nil, err
	}
	return fromInternalPermissionsConfig(cfg), nil
}

func SavePermissionsConfig(cfg *PermissionsConfig) error {
	return permissions.SaveConfig(toInternalPermissionsConfig(cfg))
}

func SavePermissionsConfigToPath(path string, cfg *PermissionsConfig) error {
	return permissions.SaveConfigToPath(path, toInternalPermissionsConfig(cfg))
}

func (c *PermissionsConfig) AddGlobalAllowedURL(target string) bool {
	return appendUniqueString(&c.GlobalAllowedURLs, target)
}

func (c *PermissionsConfig) AddPersistentAllowedURL(target string) bool {
	return appendUniqueString(&c.PersistentAllowedURLs, target)
}

func (c *PermissionsConfig) AddPersistentAllowedCommand(target string) bool {
	return appendUniqueString(&c.PersistentAllowedCommands, target)
}

func (c *PermissionsConfig) SetPersistentAllowedFile(path, access string) bool {
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

func fromInternalPermissionsConfig(cfg *permissions.Config) *PermissionsConfig {
	if cfg == nil {
		return nil
	}

	publicCfg := &PermissionsConfig{
		SandboxImage:              cfg.SandboxImage,
		ShowPermissionOverview:    cfg.ShowPermissionOverview,
		Caches:                    make(map[string]CacheConfig, len(cfg.Caches)),
		GlobalBlockedURLs:         cloneStrings(cfg.GlobalBlockedURLs),
		GlobalAllowedURLs:         cloneStrings(cfg.GlobalAllowedURLs),
		GlobalBlockedFiles:        cloneStrings(cfg.GlobalBlockedFiles),
		GlobalAllowedFiles:        cloneStrings(cfg.GlobalAllowedFiles),
		GlobalBlockedCommands:     cloneStrings(cfg.GlobalBlockedCommands),
		GlobalAllowedCommands:     cloneStrings(cfg.GlobalAllowedCommands),
		PersistentAllowedURLs:     cloneStrings(cfg.PersistentAllowedURLs),
		PersistentAllowedCommands: cloneStrings(cfg.PersistentAllowedCommands),
		PersistentAllowedFiles:    cloneStringMap(cfg.PersistentAllowedFiles),
		StrictFileAllowlist:       cfg.StrictFileAllowlist,
		StrictCommandAllowlist:    cfg.StrictCommandAllowlist,
		ApprovedPlugins:           cloneBoolMap(cfg.ApprovedPlugins),
	}

	for name, cache := range cfg.Caches {
		publicCfg.Caches[name] = CacheConfig{
			ContainerPath: cache.ContainerPath,
			Env:           cloneStrings(cache.Env),
		}
	}

	publicCfg.ensureDefaults()
	return publicCfg
}

func toInternalPermissionsConfig(cfg *PermissionsConfig) *permissions.Config {
	if cfg == nil {
		return nil
	}

	internalCfg := &permissions.Config{
		SandboxImage:              cfg.SandboxImage,
		ShowPermissionOverview:    cfg.ShowPermissionOverview,
		Caches:                    make(map[string]permissions.CacheConfig, len(cfg.Caches)),
		GlobalBlockedURLs:         cloneStrings(cfg.GlobalBlockedURLs),
		GlobalAllowedURLs:         cloneStrings(cfg.GlobalAllowedURLs),
		GlobalBlockedFiles:        cloneStrings(cfg.GlobalBlockedFiles),
		GlobalAllowedFiles:        cloneStrings(cfg.GlobalAllowedFiles),
		GlobalBlockedCommands:     cloneStrings(cfg.GlobalBlockedCommands),
		GlobalAllowedCommands:     cloneStrings(cfg.GlobalAllowedCommands),
		PersistentAllowedURLs:     cloneStrings(cfg.PersistentAllowedURLs),
		PersistentAllowedCommands: cloneStrings(cfg.PersistentAllowedCommands),
		PersistentAllowedFiles:    cloneStringMap(cfg.PersistentAllowedFiles),
		StrictFileAllowlist:       cfg.StrictFileAllowlist,
		StrictCommandAllowlist:    cfg.StrictCommandAllowlist,
		ApprovedPlugins:           cloneBoolMap(cfg.ApprovedPlugins),
	}

	for name, cache := range cfg.Caches {
		internalCfg.Caches[name] = permissions.CacheConfig{
			ContainerPath: cache.ContainerPath,
			Env:           cloneStrings(cache.Env),
		}
	}

	return internalCfg
}

func (c *PermissionsConfig) ensureDefaults() {
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

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string(nil), values...)
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func cloneBoolMap(values map[string]bool) map[string]bool {
	if values == nil {
		return nil
	}
	cloned := make(map[string]bool, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
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
