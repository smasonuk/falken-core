package conversation

import (
	"fmt"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/smasonuk/falken-core/internal/store"
)

const memorySectionHeader = "--- CURRENT AGENT MEMORY ---"

const (
	maxCurrentGoalRunes    = 500
	maxMemoryEntries       = 50
	maxMemoryEntryRunes    = 500
	maxImportantFiles      = 50
	maxImportantFileRunes  = 300
	maxMemoryDecisions     = 50
	maxMemoryDecisionRunes = 500
	maxOpenQuestions       = 50
	maxOpenQuestionRunes   = 500
)

// MemoryManager coordinates structured runtime memory validation, merging, and persistence.
type MemoryManager struct {
	store store.MemoryBackend
}

// MemoryUpdate describes a merge-style update to durable agent memory.
type MemoryUpdate struct {
	CurrentGoal          string
	ClearCurrentGoal     bool
	AddEntries           []string
	RemoveEntries        []string
	AddImportantFiles    []string
	RemoveImportantFiles []string
	AddDecisions         []string
	RemoveDecisions      []string
	AddOpenQuestions     []string
	RemoveOpenQuestions  []string
}

// NewMemoryManager creates a memory runtime helper over the conversation-scoped memory store.
func NewMemoryManager(memoryStore store.MemoryBackend) *MemoryManager {
	return &MemoryManager{store: memoryStore}
}

// Read returns normalized structured memory. Missing memory state returns an empty state.
func (m *MemoryManager) Read() (store.MemoryState, error) {
	state, err := m.store.Read()
	if err != nil {
		return store.MemoryState{}, err
	}
	state = NormalizeMemory(state)
	if err := ValidateMemory(state); err != nil {
		return store.MemoryState{}, err
	}
	return cloneMemory(state), nil
}

// Update merges a concise update into existing structured memory and persists the result.
func (m *MemoryManager) Update(update MemoryUpdate) (store.MemoryState, error) {
	current, err := m.Read()
	if err != nil {
		return store.MemoryState{}, err
	}
	next := cloneMemory(current)
	if update.ClearCurrentGoal {
		next.CurrentGoal = ""
	} else if strings.TrimSpace(update.CurrentGoal) != "" {
		next.CurrentGoal = update.CurrentGoal
	}

	next.Entries = mergeMemoryList(next.Entries, update.AddEntries, update.RemoveEntries)
	next.ImportantFiles = mergeMemoryList(next.ImportantFiles, update.AddImportantFiles, update.RemoveImportantFiles)
	next.Decisions = mergeMemoryList(next.Decisions, update.AddDecisions, update.RemoveDecisions)
	next.OpenQuestions = mergeMemoryList(next.OpenQuestions, update.AddOpenQuestions, update.RemoveOpenQuestions)
	next = NormalizeMemory(next)
	if err := ValidateMemory(next); err != nil {
		return store.MemoryState{}, err
	}
	if err := m.store.Write(next); err != nil {
		return store.MemoryState{}, err
	}
	return cloneMemory(next), nil
}

// Clear removes all current memory.
func (m *MemoryManager) Clear() error {
	return m.store.Write(store.MemoryState{
		Entries:        []string{},
		ImportantFiles: []string{},
		Decisions:      []string{},
		OpenQuestions:  []string{},
	})
}

// RenderMemory renders structured conversation memory in deterministic prompt/context form.
func RenderMemory(memory store.MemoryState) string {
	memory = NormalizeMemory(memory)
	if IsMemoryEmpty(memory) {
		return memorySectionHeader + "\n(no memory entries)"
	}

	var builder strings.Builder
	builder.WriteString(memorySectionHeader)
	renderMemorySection(&builder, "Current goal:", singleMemoryItem(memory.CurrentGoal))
	renderMemorySection(&builder, "Important files:", memory.ImportantFiles)
	renderMemorySection(&builder, "Decisions:", memory.Decisions)
	renderMemorySection(&builder, "Open questions:", memory.OpenQuestions)
	renderMemorySection(&builder, "Notes:", memory.Entries)
	return builder.String()
}

// ValidateMemory rejects memory that is too large to keep in durable context.
func ValidateMemory(memory store.MemoryState) error {
	memory = NormalizeMemory(memory)
	if runeLen(memory.CurrentGoal) > maxCurrentGoalRunes {
		return fmt.Errorf("current_goal exceeds maximum length %d", maxCurrentGoalRunes)
	}
	if err := validateMemoryList("entries", memory.Entries, maxMemoryEntries, maxMemoryEntryRunes); err != nil {
		return err
	}
	if err := validateMemoryList("important_files", memory.ImportantFiles, maxImportantFiles, maxImportantFileRunes); err != nil {
		return err
	}
	if err := validateMemoryList("decisions", memory.Decisions, maxMemoryDecisions, maxMemoryDecisionRunes); err != nil {
		return err
	}
	if err := validateMemoryList("open_questions", memory.OpenQuestions, maxOpenQuestions, maxOpenQuestionRunes); err != nil {
		return err
	}
	return nil
}

// NormalizeMemory trims strings, drops empty list entries, and dedupes lists in first-seen order.
func NormalizeMemory(memory store.MemoryState) store.MemoryState {
	return store.MemoryState{
		Entries:        normalizeMemoryList(memory.Entries),
		CurrentGoal:    strings.TrimSpace(memory.CurrentGoal),
		ImportantFiles: normalizeMemoryList(memory.ImportantFiles),
		Decisions:      normalizeMemoryList(memory.Decisions),
		OpenQuestions:  normalizeMemoryList(memory.OpenQuestions),
	}
}

// IsMemoryEmpty reports whether all structured memory fields are empty.
func IsMemoryEmpty(memory store.MemoryState) bool {
	memory = NormalizeMemory(memory)
	return memory.CurrentGoal == "" &&
		len(memory.Entries) == 0 &&
		len(memory.ImportantFiles) == 0 &&
		len(memory.Decisions) == 0 &&
		len(memory.OpenQuestions) == 0
}

// MemoryEqual compares normalized memory values.
func MemoryEqual(a, b store.MemoryState) bool {
	return reflect.DeepEqual(NormalizeMemory(a), NormalizeMemory(b))
}

func renderMemorySection(builder *strings.Builder, title string, items []string) {
	if len(items) == 0 {
		return
	}
	builder.WriteString("\n")
	builder.WriteString(title)
	for _, item := range items {
		builder.WriteString("\n- ")
		builder.WriteString(renderMemoryField(item))
	}
}

func renderMemoryField(value string) string {
	value = strings.ReplaceAll(value, "\r\n", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\r", " ")
	return strings.TrimSpace(value)
}

func singleMemoryItem(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return []string{value}
}

func validateMemoryList(field string, values []string, maxItems, maxRunes int) error {
	if len(values) > maxItems {
		return fmt.Errorf("%s has too many items: %d exceeds maximum %d", field, len(values), maxItems)
	}
	for i, value := range values {
		if runeLen(value) > maxRunes {
			return fmt.Errorf("%s item %d exceeds maximum length %d", field, i, maxRunes)
		}
	}
	return nil
}

func normalizeMemoryList(values []string) []string {
	normalized := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	if normalized == nil {
		return []string{}
	}
	return normalized
}

func mergeMemoryList(existing, add, remove []string) []string {
	removeSet := make(map[string]struct{}, len(remove))
	for _, value := range normalizeMemoryList(remove) {
		removeSet[value] = struct{}{}
	}

	values := make([]string, 0, len(existing)+len(add))
	for _, value := range append(normalizeMemoryList(existing), normalizeMemoryList(add)...) {
		if _, remove := removeSet[value]; remove {
			continue
		}
		values = append(values, value)
	}
	return normalizeMemoryList(values)
}

func cloneMemory(memory store.MemoryState) store.MemoryState {
	memory = NormalizeMemory(memory)
	memory.Entries = append([]string(nil), memory.Entries...)
	memory.ImportantFiles = append([]string(nil), memory.ImportantFiles...)
	memory.Decisions = append([]string(nil), memory.Decisions...)
	memory.OpenQuestions = append([]string(nil), memory.OpenQuestions...)
	return memory
}

func runeLen(value string) int {
	return utf8.RuneCountInString(value)
}
