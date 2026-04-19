package agent

import "github.com/sashabaranov/go-openai"

type modePolicy struct{}

func (p *modePolicy) toolsForCurrentMode(r *Runner) []openai.Tool {
	if r.Mode == ModeDefault {
		return r.tools
	}

	var allowed []string
	switch r.Mode {
	case ModePlan:
		allowed = []string{
			"read_file", "read_files", "glob", "grep", "search_tools",
			"TaskCreate", "TaskList", "TaskGet", "TaskUpdate",
			"update_memory", "enter_plan_mode", "exit_plan_mode",
			"write_plan", "read_plan",
		}
	case ModeVerify:
		allowed = []string{"read_file", "read_files", "glob", "grep", "execute_command"}
	case ModeExplore:
		allowed = []string{"read_file", "read_files", "glob", "grep", "search_tools"}
	}

	allowedMap := make(map[string]bool, len(allowed))
	for _, a := range allowed {
		allowedMap[a] = true
	}

	var filtered []openai.Tool
	for _, t := range r.tools {
		if allowedMap[t.Function.Name] {
			filtered = append(filtered, t)
		}
	}
	return filtered
}

func (p *modePolicy) blockedToolMessage(r *Runner, toolName string, args map[string]any) (string, bool) {
	if r.Mode != ModePlan && r.Mode != ModeVerify && r.Mode != ModeExplore {
		return "", false
	}

	isWrite := false
	switch toolName {
	case "write_file", "edit_file", "multi_edit", "apply_patch":
		isWrite = true
	case "execute_command", "start_background_process", "kill_process":
		if r.Mode != ModeVerify {
			isWrite = true
		}
	}

	if !isWrite {
		return "", false
	}

	switch r.Mode {
	case ModePlan:
		return "You are currently in Plan Mode. You cannot modify files or execute commands. Write your plan using the 'write_plan' tool, and use the 'exit_plan_mode' tool to finish planning.", true
	case ModeVerify:
		return "You are currently in Verify Mode. You cannot modify files, only execute test commands and read files.", true
	case ModeExplore:
		return "You are currently in Explore Mode. You are restricted to read-only tools like read_file, glob, grep.", true
	default:
		return "This tool is not allowed in the current operating mode.", true
	}
}
