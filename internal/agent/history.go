package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/smasonuk/falken-core/internal/store"
)

const planSectionHeader = "--- CURRENT PLAN ---"
const maxInlinePlanBytes = 1200

// HistoryManager assembles agent-runtime messages from conversation-scoped stores.
type HistoryManager struct {
	history store.HistoryBackend
	memory  store.MemoryBackend
	todos   *TodoManager
	mode    *ModeState
	compact MessageCompactor
}

// HistoryManagerOption configures a HistoryManager.
type HistoryManagerOption func(*HistoryManager)

// NewHistoryManager creates the canonical agent history/memory orchestration layer.
func NewHistoryManager(history store.HistoryBackend, memory store.MemoryBackend, opts ...HistoryManagerOption) *HistoryManager {
	manager := &HistoryManager{
		history: history,
		memory:  memory,
		mode:    &ModeState{mode: ModeDefault},
		compact: NoOpCompactor{},
	}
	for _, opt := range opts {
		opt(manager)
	}
	if manager.compact == nil {
		manager.compact = NoOpCompactor{}
	}

	return manager
}

// WithCompactor installs the compaction seam used before messages are returned and persisted.
func WithCompactor(compactor MessageCompactor) HistoryManagerOption {
	return func(manager *HistoryManager) {
		manager.compact = compactor
	}
}

// WithTodoStore enables todo context integration for prepared run messages.
func WithTodoStore(todoStore store.TodoBackend) HistoryManagerOption {
	return func(manager *HistoryManager) {
		manager.todos = NewTodoManager(todoStore)
	}
}

// WithPlanStore enables plan-backed mode operations for the history manager.
func WithPlanStore(planStore store.PlanBackend) HistoryManagerOption {
	return func(manager *HistoryManager) {
		manager.mode = NewModeState(planStore)
	}
}

// WithModeState installs shared mode state for prepared run context.
func WithModeState(mode *ModeState) HistoryManagerOption {
	return func(manager *HistoryManager) {
		manager.mode = mode
	}
}

// MessageCompactor is the seam for future summarization or truncation policies.
type MessageCompactor interface {
	Compact([]Message) ([]Message, error)
}

// MessageCompactorFunc adapts a function into a MessageCompactor.
type MessageCompactorFunc func([]Message) ([]Message, error)

// Compact applies the function-backed compaction policy.
func (f MessageCompactorFunc) Compact(messages []Message) ([]Message, error) {
	return f(messages)
}

// NoOpCompactor preserves all messages while still making future compaction explicit.
type NoOpCompactor struct{}

// Compact returns a defensive copy of messages without changing ordering or content.
func (NoOpCompactor) Compact(messages []Message) ([]Message, error) {
	return cloneMessages(messages), nil
}

// PrepareRunRequest captures the inputs needed to assemble the next LLM message list.
type PrepareRunRequest struct {
	BaseSystemPrompt string
	UserPrompt       string
}

// Load returns persisted agent messages in order. Missing history returns an empty slice.
func (m *HistoryManager) Load() ([]Message, error) {
	entries, err := m.history.Read()
	if err != nil {
		return nil, err
	}

	messages := make([]Message, 0, len(entries))
	for i, entry := range entries {
		message, err := decodeHistoryMessage(entry)
		if err != nil {
			return nil, fmt.Errorf("decode history entry %d: %w", i, err)
		}
		messages = append(messages, message)
	}

	return messages, nil
}

// Append persists a single new conversation message after validating existing history.
func (m *HistoryManager) Append(message Message) error {
	messages, err := m.Load()
	if err != nil {
		return err
	}

	messages = append(messages, cloneMessage(message))
	return m.write(messages)
}

// ReadTodos returns the current runtime todos when todo context is enabled.
func (m *HistoryManager) ReadTodos() ([]Todo, error) {
	if m == nil || m.todos == nil {
		return nil, nil
	}
	return m.todos.Read()
}

// ReadPlan returns the current active plan text when plan context is enabled.
func (m *HistoryManager) ReadPlan() (string, error) {
	if m == nil || m.mode == nil || m.mode.Plan() == nil {
		return "", nil
	}
	return m.mode.Plan().Read()
}

// PrepareRun loads history and memory, refreshes the system prompt, appends the user prompt,
// applies the configured compaction policy, and persists the prepared message list.
func (m *HistoryManager) PrepareRun(request PrepareRunRequest) ([]Message, error) {
	messages, err := m.Load()
	if err != nil {
		return nil, err
	}

	memory, err := m.memory.Read()
	if err != nil {
		return nil, err
	}

	var todos []Todo
	if m.todos != nil {
		todos, err = m.todos.Read()
		if err != nil {
			return nil, err
		}
		if todos == nil {
			todos = []Todo{}
		}
	}
	activePlan := ""
	if m.mode != nil && m.mode.Plan() != nil {
		plan, err := m.mode.Plan().Read()
		if err != nil {
			return nil, err
		}
		if isActivePlanText(plan) {
			activePlan = plan
		}
	}

	system := SystemMessage(RenderSystemPromptWithPlan(request.BaseSystemPrompt, memory, activePlan, todos...))
	if len(messages) > 0 && messages[0].Role == RoleSystem {
		messages[0] = system
	} else {
		messages = append([]Message{system}, messages...)
	}
	messages = append(messages, UserMessage(request.UserPrompt))

	compacted, err := m.compact.Compact(messages)
	if err != nil {
		return nil, fmt.Errorf("compact history: %w", err)
	}
	if err := m.write(compacted); err != nil {
		return nil, err
	}

	return cloneMessages(compacted), nil
}

func isActivePlanText(plan string) bool {
	trimmed := strings.TrimSpace(plan)
	return trimmed != "" && trimmed != strings.TrimSpace(defaultPlanStarterText)
}

// RenderSystemPrompt combines the caller's base system prompt with deterministic runtime context.
func RenderSystemPrompt(baseSystemPrompt string, memory store.MemoryState, todos ...Todo) string {
	return RenderSystemPromptWithPlan(baseSystemPrompt, memory, "", todos...)
}

// RenderSystemPromptWithPlan combines the caller's base system prompt with
// deterministic runtime context, including a compact active-plan hint.
func RenderSystemPromptWithPlan(baseSystemPrompt string, memory store.MemoryState, activePlan string, todos ...Todo) string {
	base := strings.TrimRight(baseSystemPrompt, "\n")
	contextSections := []string{RenderMemory(memory)}
	if strings.TrimSpace(activePlan) != "" {
		contextSections = append(contextSections, RenderActivePlan(activePlan))
	}
	if todos != nil {
		contextSections = append(contextSections, RenderTodos(todos))
	}
	runtimeContext := strings.Join(contextSections, "\n\n")
	if base == "" {
		return runtimeContext
	}

	return base + "\n\n" + runtimeContext
}

// RenderActivePlan renders small active plans inline and large active plans as
// a reminder that exact details are available through read_plan.
func RenderActivePlan(plan string) string {
	trimmed := strings.TrimSpace(plan)
	if trimmed == "" {
		return planSectionHeader + "\n(no active plan)"
	}
	if len([]byte(trimmed)) <= maxInlinePlanBytes {
		return planSectionHeader + "\n" + trimmed
	}
	return planSectionHeader + "\n(active plan exists; use read_plan if you need exact details)"
}

func (m *HistoryManager) currentMode() Mode {
	if m == nil || m.mode == nil {
		return ModeDefault
	}

	return m.mode.Current()
}

func (m *HistoryManager) write(messages []Message) error {
	entries := make([]string, 0, len(messages))
	for i, message := range messages {
		entry, err := encodeHistoryMessage(message)
		if err != nil {
			return fmt.Errorf("encode history message %d: %w", i, err)
		}
		entries = append(entries, entry)
	}

	return m.history.Write(entries)
}

func encodeHistoryMessage(message Message) (string, error) {
	data, err := json.Marshal(cloneMessage(message))
	if err != nil {
		return "", err
	}

	return string(data), nil
}

func decodeHistoryMessage(entry string) (Message, error) {
	var message Message
	if err := json.Unmarshal([]byte(entry), &message); err != nil {
		return Message{}, err
	}

	return cloneMessage(message), nil
}

func cloneMessages(messages []Message) []Message {
	cloned := make([]Message, 0, len(messages))
	for _, message := range messages {
		cloned = append(cloned, cloneMessage(message))
	}

	return cloned
}

func cloneMessage(message Message) Message {
	message.ToolCalls = cloneToolCalls(message.ToolCalls)
	if message.ToolResult != nil {
		cloned := cloneToolResult(*message.ToolResult)
		message.ToolResult = &cloned
	}

	return message
}
