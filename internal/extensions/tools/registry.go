package tools

import (
	"errors"
	"sort"

	"github.com/smasonuk/falken-core/internal/extensions/manifest"
	"github.com/smasonuk/falken-core/internal/policy"
)

// ErrDuplicateTool indicates a registry already contains a tool with the same runtime name.
var ErrDuplicateTool = errors.New("duplicate tool name")

// Entry is validated metadata for one registered tool definition.
type Entry struct {
	Name            string
	Description     string
	InputSchema     []byte
	Category        string
	Keywords        []string
	AlwaysLoad      bool
	DefaultLoad     bool
	PackageName     string
	PackageDesc     string
	SourceDir       string
	ManifestPath    string
	WasmPath        string
	Runtime         manifest.RuntimeKind
	Safety          Safety
	Permissions     manifest.DeclaredPermissions
	ManifestVersion int
}

// Safety describes high-level tool capabilities used by core mode policy.
type Safety struct {
	PlanSafe         bool
	ReadsWorkspace   bool
	MutatesWorkspace bool
	ExecutesShell    bool
	UsesNetwork      bool
	UsesHostState    bool
	ReadsHostState   bool
	MutatesHostState bool
}

// Registry stores validated tool metadata. It does not activate or execute tools.
type Registry struct {
	entries map[string]Entry
}

// NewRegistry creates an empty explicit tool registry.
func NewRegistry() *Registry {
	return &Registry{entries: make(map[string]Entry)}
}

// Register stores one validated tool entry, rejecting duplicate runtime names.
func (r *Registry) Register(entry Entry) error {
	if r.entries == nil {
		r.entries = make(map[string]Entry)
	}
	if _, ok := r.entries[entry.Name]; ok {
		return DuplicateToolError{Name: entry.Name}
	}

	r.entries[entry.Name] = CloneEntry(entry)
	return nil
}

// Lookup returns a registered tool by name.
func (r *Registry) Lookup(name string) (Entry, bool) {
	if r == nil {
		return Entry{}, false
	}
	entry, ok := r.entries[name]
	if !ok {
		return Entry{}, false
	}
	return CloneEntry(entry), true
}

// List returns registered tools in deterministic name order.
func (r *Registry) List() []Entry {
	if r == nil {
		return nil
	}

	names := make([]string, 0, len(r.entries))
	for name := range r.entries {
		names = append(names, name)
	}
	sort.Strings(names)

	entries := make([]Entry, 0, len(names))
	for _, name := range names {
		entries = append(entries, CloneEntry(r.entries[name]))
	}
	return entries
}

// Count returns the number of registered tools.
func (r *Registry) Count() int {
	if r == nil {
		return 0
	}
	return len(r.entries)
}

// DuplicateToolError identifies a duplicate runtime tool name.
type DuplicateToolError struct {
	Name string
}

func (e DuplicateToolError) Error() string {
	return ErrDuplicateTool.Error() + ": " + e.Name
}

// Is lets callers use errors.Is(err, ErrDuplicateTool).
func (e DuplicateToolError) Is(target error) bool {
	return target == ErrDuplicateTool
}

// CloneEntry returns a deep copy of one tool entry, including schema,
// keyword, permission, and per-file mode slices.
func CloneEntry(entry Entry) Entry {
	entry.InputSchema = append([]byte(nil), entry.InputSchema...)
	entry.Keywords = append([]string(nil), entry.Keywords...)
	entry.Permissions = clonePermissions(entry.Permissions)
	return entry
}

// CloneEntries returns a deep copy of a tool entry slice.
func CloneEntries(entries []Entry) []Entry {
	cloned := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		cloned = append(cloned, CloneEntry(entry))
	}
	return cloned
}

func clonePermissions(permissions manifest.DeclaredPermissions) manifest.DeclaredPermissions {
	cloned := manifest.DeclaredPermissions{
		Files:   append([]manifest.FilePermission(nil), permissions.Files...),
		Network: append([]manifest.NetworkPermission(nil), permissions.Network...),
		Shell:   append([]manifest.ShellPermission(nil), permissions.Shell...),
	}
	for i := range cloned.Files {
		cloned.Files[i].Modes = append([]policy.FileAccessMode(nil), permissions.Files[i].Modes...)
	}
	return cloned
}
