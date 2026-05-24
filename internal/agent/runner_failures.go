package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
)

var (
	// ErrRepeatedToolFailure indicates a tool failed identically enough times to trip the loop guard.
	ErrRepeatedToolFailure = errors.New("repeated identical tool failure")
)

type repeatedFailure struct {
	Count  int
	Status string
	Error  string
}

// repeatedFailureTracker detects consecutive identical failed tool calls within
// a single Runner.Run invocation.
//
// It intentionally does not count failures across successful tool calls or
// across different failure fingerprints. Iterative coding workflows often run
// the same verification command multiple times while fixing different
// underlying errors, so command failures include an output fingerprint.
type repeatedFailureTracker struct {
	lastKey string
	last    repeatedFailure
}

func newRepeatedFailureTracker() *repeatedFailureTracker {
	return &repeatedFailureTracker{}
}

func (t *repeatedFailureTracker) Record(call ToolCall, result ToolResult) repeatedFailure {
	if t == nil {
		return repeatedFailure{}
	}

	failureText := toolResultFailureText(result)
	if failureText == "" {
		t.Reset()
		return repeatedFailure{}
	}

	status := toolResultStatus(result)
	key := repeatedFailureKey(call, result, status, failureText)

	if key != t.lastKey {
		t.lastKey = key
		t.last = repeatedFailure{
			Count:  1,
			Status: status,
			Error:  failureText,
		}
		return t.last
	}

	t.last.Count++
	t.last.Status = status
	t.last.Error = failureText
	return t.last
}

func (t *repeatedFailureTracker) Reset() {
	if t == nil {
		return
	}
	t.lastKey = ""
	t.last = repeatedFailure{}
}

func toolResultFailureText(result ToolResult) string {
	if strings.TrimSpace(result.Error) != "" {
		return strings.TrimSpace(result.Error)
	}

	if len(result.Payload) != 0 {
		var payload struct {
			Success *bool  `json:"success"`
			Error   string `json:"error"`
			Status  string `json:"status"`
		}
		if err := json.Unmarshal(result.Payload, &payload); err == nil {
			if payload.Success != nil && !*payload.Success {
				if strings.TrimSpace(payload.Error) != "" {
					return strings.TrimSpace(payload.Error)
				}
				if strings.TrimSpace(result.Content) != "" {
					return strings.TrimSpace(result.Content)
				}
				if strings.TrimSpace(payload.Status) != "" {
					return strings.TrimSpace(payload.Status)
				}
				return "tool reported failure"
			}
		}
	}

	return ""
}

func repeatedFailureKey(call ToolCall, result ToolResult, status, failureText string) string {
	name := result.Name
	if strings.TrimSpace(name) == "" {
		name = call.Name
	}

	key := name + "\x00" + status + "\x00" + failureText
	if name == "execute_command" {
		return key + "\x00" + executeCommandFailureIdentity(call, result)
	}
	return key + "\x00" + jsonFingerprint(call.Arguments)
}

func jsonFingerprint(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		trimmed = "{}"
	}

	var decoded any
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return sha256Hex([]byte(trimmed))
	}

	canonical, err := json.Marshal(decoded)
	if err != nil {
		return sha256Hex([]byte(trimmed))
	}

	return sha256Hex(canonical)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

var (
	commandDurationPattern = regexp.MustCompile(`\b\d+(?:\.\d+)?(?:ns|µs|us|ms|s|m|h)\b`)
	ansiEscapePattern      = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
)

const (
	maxFailureFingerprintLines     = 200
	maxFailureFingerprintHeadLines = 100
	maxFailureFingerprintTailLines = 100
	maxFailureFingerprintLineBytes = 1000
)

func executeCommandFailureIdentity(call ToolCall, result ToolResult) string {
	var payload struct {
		Command        string `json:"command"`
		WorkingDir     string `json:"working_dir"`
		ToolWorkingDir string `json:"tool_working_dir"`
		ExitCode       int    `json:"exit_code"`
		Stdout         string `json:"stdout"`
		Stderr         string `json:"stderr"`
		CombinedOutput string `json:"combined_output"`
	}
	if len(result.Payload) != 0 {
		_ = json.Unmarshal(result.Payload, &payload)
	}

	var args struct {
		Command    string            `json:"command"`
		WorkingDir string            `json:"working_dir"`
		Env        map[string]string `json:"env"`
	}
	if len(call.Arguments) != 0 {
		_ = json.Unmarshal(call.Arguments, &args)
	}

	command := payload.Command
	if strings.TrimSpace(command) == "" {
		command = args.Command
	}
	command = strings.Join(strings.Fields(command), " ")

	workingDir := payload.ToolWorkingDir
	if strings.TrimSpace(workingDir) == "" {
		workingDir = payload.WorkingDir
	}
	if strings.TrimSpace(workingDir) == "" {
		workingDir = args.WorkingDir
	}
	workingDir = strings.TrimSpace(workingDir)

	output := payload.CombinedOutput
	if output == "" {
		output = payload.Stdout + "\n" + payload.Stderr
	}

	return strings.Join([]string{
		"command=" + command,
		"working_dir=" + workingDir,
		"exit_code=" + strconv.Itoa(payload.ExitCode),
		"env=" + stringMapFingerprint(args.Env),
		"output=" + commandOutputFingerprint(output),
	}, "\x00")
}

func stringMapFingerprint(values map[string]string) string {
	if len(values) == 0 {
		return "empty"
	}
	data, err := json.Marshal(values)
	if err != nil {
		return "invalid"
	}
	return sha256Hex(data)
}

func commandOutputFingerprint(output string) string {
	normalized := normalizeCommandFailureOutput(output)
	if normalized == "" {
		return "empty"
	}
	return sha256Hex([]byte(normalized))
}

func normalizeCommandFailureOutput(output string) string {
	output = strings.ReplaceAll(output, "\r\n", "\n")
	output = strings.ReplaceAll(output, "\r", "\n")
	output = ansiEscapePattern.ReplaceAllString(output, "")

	rawLines := strings.Split(output, "\n")
	lines := make([]string, 0, len(rawLines))

	for _, line := range rawLines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		line = commandDurationPattern.ReplaceAllString(line, "<duration>")
		line = truncateFingerprintLine(line, maxFailureFingerprintLineBytes)

		lines = append(lines, line)
	}

	if len(lines) > maxFailureFingerprintLines {
		head := append([]string(nil), lines[:maxFailureFingerprintHeadLines]...)
		tail := append([]string(nil), lines[len(lines)-maxFailureFingerprintTailLines:]...)
		lines = append(append(head, "<omitted>"), tail...)
	}

	return strings.Join(lines, "\n")
}

func truncateFingerprintLine(line string, maxBytes int) string {
	if maxBytes <= 0 || len([]byte(line)) <= maxBytes {
		return line
	}

	data := []byte(line)
	truncated := string(data[:maxBytes])
	return strings.ToValidUTF8(truncated, "\uFFFD") + "...<truncated>"
}

func withRepeatedFailureWarning(result ToolResult) ToolResult {
	const warning = "This tool has failed repeatedly with the same error. Do not retry the same call unchanged."
	failureText := toolResultFailureText(result)
	if !strings.Contains(result.Content, warning) {
		if result.Content == "" {
			result.Content = warning
		} else {
			result.Content += "\n\n" + warning
		}
	}
	if strings.TrimSpace(result.Error) == "" && failureText != "" {
		result.Error = failureText
	}
	if !strings.Contains(result.Error, warning) {
		if result.Error == "" {
			result.Error = warning
		} else {
			result.Error += "\n\n" + warning
		}
	}
	if len(result.Payload) != 0 {
		var payload map[string]any
		if err := json.Unmarshal(result.Payload, &payload); err == nil {
			payload["repeated_failure_warning"] = warning
			result.Payload = marshalToolPayload(payload)
		}
	}
	return result
}
