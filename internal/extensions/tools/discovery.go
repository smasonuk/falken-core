package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/smasonuk/falken-core/internal/extensions/manifest"
	"github.com/smasonuk/falken-core/internal/policy"
)

const (
	// ManifestFileName is the v1 tool package manifest filename.
	ManifestFileName = "tool.json"
	// WasmFileName is the v1 convention-based Wasm binary path recorded for later activation.
	WasmFileName = "main.wasm"
)

// DiscoverOptions configures filesystem tool discovery.
type DiscoverOptions struct {
	Roots []string
}

// DiscoveryResult reports discovered, invalid, skipped, and registered tool metadata.
type DiscoveryResult struct {
	Registered []Entry
	Invalid    []InvalidManifest
	Skipped    []SkippedPath
}

// InvalidManifest reports one manifest that could not be parsed, validated, or registered.
type InvalidManifest struct {
	Path  string
	Error string
}

// SkippedPath reports a filesystem entry that was intentionally ignored.
type SkippedPath struct {
	Path   string
	Reason string
}

// Discover scans configured roots and registers valid tool manifests into a new registry.
func Discover(options DiscoverOptions) (*Registry, DiscoveryResult, error) {
	registry := NewRegistry()
	result, err := DiscoverInto(registry, options)
	return registry, result, err
}

// DiscoverInto scans configured roots and registers valid tool manifests into the supplied registry.
func DiscoverInto(registry *Registry, options DiscoverOptions) (DiscoveryResult, error) {
	if registry == nil {
		registry = NewRegistry()
	}

	result := DiscoveryResult{}
	roots := append([]string(nil), options.Roots...)
	sort.Strings(roots)

	for _, root := range roots {
		if err := discoverRoot(registry, filepath.Clean(root), &result); err != nil {
			return DiscoveryResult{}, err
		}
	}

	return result, nil
}

func discoverRoot(registry *Registry, root string, result *DiscoveryResult) error {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		result.Skipped = append(result.Skipped, SkippedPath{
			Path:   root,
			Reason: "tool root does not exist",
		})
		return nil
	}
	if err != nil {
		return fmt.Errorf("read tool root %q: %w", root, err)
	}

	for _, entry := range entries {
		path := filepath.Join(root, entry.Name())
		if !entry.IsDir() {
			result.Skipped = append(result.Skipped, SkippedPath{
				Path:   path,
				Reason: "not a tool package directory",
			})
			continue
		}

		if err := discoverPackageDir(registry, path, result); err != nil {
			return err
		}
	}

	return nil
}

func discoverPackageDir(registry *Registry, dir string, result *DiscoveryResult) error {
	manifestPath := filepath.Join(dir, ManifestFileName)
	stat, err := os.Stat(manifestPath)
	if errors.Is(err, os.ErrNotExist) {
		result.Skipped = append(result.Skipped, SkippedPath{
			Path:   dir,
			Reason: "missing " + ManifestFileName,
		})
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat tool manifest %q: %w", manifestPath, err)
	}
	if stat.IsDir() {
		result.Invalid = append(result.Invalid, InvalidManifest{
			Path:  manifestPath,
			Error: "tool manifest path is a directory",
		})
		return nil
	}

	// #nosec G304 -- manifestPath is convention-derived from a configured discovery root.
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read tool manifest %q: %w", manifestPath, err)
	}

	parsed, invalid := parseDiscoveredToolManifest(data, manifestPath)
	if invalid.Error != "" {
		result.Invalid = append(result.Invalid, invalid)
		return nil
	}

	for _, tool := range parsed.Tools {
		entry := entryFromManifest(parsed, tool, dir, manifestPath)
		invalid := registerDiscoveredTool(registry, entry, manifestPath)
		if invalid.Error != "" {
			result.Invalid = append(result.Invalid, invalid)
			return nil
		}
		result.Registered = append(result.Registered, entry)
	}

	return nil
}

func parseDiscoveredToolManifest(data []byte, path string) (manifest.ToolManifest, InvalidManifest) {
	parsed, err := manifest.ParseToolManifest(data)
	if err != nil {
		return manifest.ToolManifest{}, InvalidManifest{
			Path:  path,
			Error: err.Error(),
		}
	}
	return parsed, InvalidManifest{}
}

func registerDiscoveredTool(registry *Registry, entry Entry, manifestPath string) InvalidManifest {
	if err := registry.Register(entry); err != nil {
		return InvalidManifest{
			Path:  manifestPath,
			Error: err.Error(),
		}
	}
	return InvalidManifest{}
}

func entryFromManifest(parsed manifest.ToolManifest, tool manifest.ToolDefinition, sourceDir, manifestPath string) Entry {
	return Entry{
		Name:            tool.Name,
		Description:     tool.Description,
		InputSchema:     append([]byte(nil), tool.InputSchema...),
		Category:        tool.Category,
		Keywords:        append([]string(nil), tool.Keywords...),
		AlwaysLoad:      tool.AlwaysLoad,
		DefaultLoad:     tool.DefaultLoad,
		PackageName:     parsed.Name,
		PackageDesc:     parsed.Description,
		SourceDir:       filepath.Clean(sourceDir),
		ManifestPath:    filepath.Clean(manifestPath),
		WasmPath:        filepath.Join(sourceDir, WasmFileName),
		Runtime:         parsed.Runtime,
		Safety:          deriveSafety(parsed.Permissions),
		Permissions:     clonePermissions(parsed.Permissions),
		ManifestVersion: parsed.ManifestVersion,
	}
}

func deriveSafety(permissions manifest.DeclaredPermissions) Safety {
	safety := Safety{
		ReadsWorkspace: len(permissions.Files) > 0,
		ExecutesShell:  len(permissions.Shell) > 0,
		UsesNetwork:    len(permissions.Network) > 0,
	}
	for _, file := range permissions.Files {
		if len(file.Modes) == 0 {
			safety.MutatesWorkspace = true
			continue
		}
		for _, mode := range file.Modes {
			switch mode {
			case policy.FileAccessWrite, policy.FileAccessCreate:
				safety.MutatesWorkspace = true
			}
		}
	}
	return safety
}
