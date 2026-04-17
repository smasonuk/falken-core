package agent

// AgentThoughtMsg represents a thought or reasoning step from the agent.
type AgentThoughtMsg struct {
	Text string
}

// AgentTextMsg represents a direct text message from the agent to the user.
type AgentTextMsg struct {
	Text string
}

// ToolCallMsg represents a request by the agent to call a specific tool.
type ToolCallMsg struct {
	Name string
	Args map[string]any
}

// ToolResultMsg represents the outcome of a tool execution.
type ToolResultMsg struct {
	Name   string
	Result map[string]any
}

// AgentDoneMsg signals that the agent has finished its execution loop.
type AgentDoneMsg struct {
	Error error
}

// CommandStreamMsg represents a live chunk of output from a running command.
type CommandStreamMsg struct {
	Chunk string
}
