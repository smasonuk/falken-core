package falken_test

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/smasonuk/falken-core/pkg/falken"
)

func TestSessionE2E_TextOnlyRun(t *testing.T) {
	setStateHomeEnv(t)

	llm := &publicFakeLLM{responses: []falken.CompletionResponse{{
		AssistantText: "hello from the assembled runtime",
		FinishReason:  falken.FinishReasonStop,
	}}}
	var events []falken.Event
	var completed falken.RunResult
	completionCalls := 0
	session, err := falken.New(falken.Config{
		WorkspaceDir:     publicTempWorkspace(t),
		ExecutionDetails: falken.ExecutionConfig{Mode: falken.ExecutionModeLocal},
		LLM:              llm,
		BaseSystemPrompt: "You are Falken.",
		Events:           func(event falken.Event) { events = append(events, event) },
		OnCompleted: func(_ context.Context, result falken.RunResult) error {
			completionCalls++
			completed = result
			return nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	result, err := session.Run(context.Background(), falken.RunRequest{Prompt: "say hello"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Completed || result.FinalOutput != "hello from the assembled runtime" {
		t.Fatalf("result = %+v, want final assistant output", result)
	}
	if completionCalls != 1 || completed.FinalOutput != result.FinalOutput {
		t.Fatalf("completion callback calls/result = %d/%+v, want one matching callback", completionCalls, completed)
	}
	if got := publicEventTypes(events); !reflect.DeepEqual(got, []falken.EventType{falken.EventPlanRouting, falken.EventAssistantText, falken.EventRunCompleted}) {
		t.Fatalf("events = %v, want assistant_text then run_completed", got)
	}
	if events[1].Text != result.FinalOutput || events[2].RunResult == nil || events[2].RunResult.FinalOutput != result.FinalOutput {
		t.Fatalf("event payloads = %+v, want assistant text before completed result", events)
	}

	history := readPublicHistory(t, session.Paths.HistoryPath)
	if got := publicMessageRoles(history); !reflect.DeepEqual(got, []falken.Role{falken.RoleSystem, falken.RoleUser, falken.RoleAssistant}) {
		t.Fatalf("history roles = %v, want system/user/assistant", got)
	}
	if history[1].Content != "say hello" || history[2].Content != result.FinalOutput {
		t.Fatalf("history = %+v, want prompt and final assistant output", history)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestSessionE2E_ToolCallRun(t *testing.T) {
	setStateHomeEnv(t)

	llm := &publicFakeLLM{responses: []falken.CompletionResponse{
		{
			ToolCalls: []falken.ToolCall{{
				ID:        "call-1",
				Name:      "external_reader",
				Arguments: json.RawMessage(`{"path":"README.md"}`),
			}},
			FinishReason: falken.FinishReasonToolCalls,
		},
		{
			AssistantText: "the tool returned content",
			FinishReason:  falken.FinishReasonStop,
		},
	}}
	var events []falken.Event
	session, err := falken.New(falken.Config{
		WorkspaceDir:     publicTempWorkspace(t),
		ExecutionDetails: falken.ExecutionConfig{Mode: falken.ExecutionModeLocal},
		ToolProviders: []falken.ToolProvider{falken.StaticToolProvider(falken.ToolFunc(falken.ToolDescriptor{
			Name:        "external_reader",
			Description: "Reads external content.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
			DefaultLoad: true,
			Safety:      falken.ToolSafety{ReadsWorkspace: true},
		}, func(context.Context, falken.ToolInvocation) (falken.ToolExecutionResult, error) {
			return falken.ToolExecutionResult{Success: true, Status: "succeeded", Content: "from native tool"}, nil
		}))},
		LLM:    llm,
		Events: func(event falken.Event) { events = append(events, event) },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	result, err := session.Run(context.Background(), falken.RunRequest{Prompt: "use the reader"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Completed || result.FinalOutput != "the tool returned content" {
		t.Fatalf("result = %+v, want final output after tool round", result)
	}
	if got := publicEventTypes(events); !reflect.DeepEqual(got, []falken.EventType{
		falken.EventPlanRouting,
		falken.EventToolCall,
		falken.EventToolResult,
		falken.EventAssistantText,
		falken.EventRunCompleted,
	}) {
		t.Fatalf("events = %v, want tool_call/tool_result/assistant_text/run_completed", got)
	}
	if events[1].ToolCall == nil || events[1].ToolCall.Name != "external_reader" {
		t.Fatalf("tool call event = %+v, want external_reader", events[1])
	}
	if events[2].ToolResult == nil || !strings.Contains(events[2].ToolResult.Content, "from native tool") {
		t.Fatalf("tool result event = %+v, want native tool output", events[2])
	}
	if len(llm.requests) != 2 {
		t.Fatalf("LLM request count = %d, want initial turn and post-tool turn", len(llm.requests))
	}
	if got := publicToolNames(llm.requests[0].Tools); !publicHasTool(got, "read_file") || !publicHasTool(got, "execute_command") || !publicHasTool(got, "external_reader") {
		t.Fatalf("initial exposed tools = %v, want built-ins plus external_reader", got)
	}

	history := readPublicHistory(t, session.Paths.HistoryPath)
	if got := publicMessageRoles(history); !reflect.DeepEqual(got, []falken.Role{
		falken.RoleSystem,
		falken.RoleUser,
		falken.RoleAssistant,
		falken.RoleTool,
		falken.RoleAssistant,
	}) {
		t.Fatalf("history roles = %v, want system/user/assistant/tool/assistant", got)
	}
	if history[3].ToolResult == nil || !strings.Contains(history[3].ToolResult.Content, "from native tool") {
		t.Fatalf("tool history message = %+v, want persisted tool result", history[3])
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestSessionE2E_ResetThenRunAgain(t *testing.T) {
	setStateHomeEnv(t)

	llm := &publicFakeLLM{responses: []falken.CompletionResponse{
		{AssistantText: "first answer", FinishReason: falken.FinishReasonStop},
		{AssistantText: "second answer", FinishReason: falken.FinishReasonStop},
	}}
	session, err := falken.New(falken.Config{
		WorkspaceDir:     publicTempWorkspace(t),
		ExecutionDetails: falken.ExecutionConfig{Mode: falken.ExecutionModeLocal},
		LLM:              llm,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := session.Run(context.Background(), falken.RunRequest{Prompt: "first"}); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if history := readPublicHistory(t, session.Paths.HistoryPath); len(history) != 3 {
		t.Fatalf("history after first run length = %d, want 3", len(history))
	}

	metadataBefore, err := os.ReadFile(session.Paths.MetadataPath)
	if err != nil {
		t.Fatalf("read metadata before reset: %v", err)
	}
	if err := session.ResetConversationState(); err != nil {
		t.Fatalf("ResetConversationState: %v", err)
	}
	if history := readPublicHistory(t, session.Paths.HistoryPath); len(history) != 0 {
		t.Fatalf("history after reset = %+v, want empty", history)
	}
	metadataAfter, err := os.ReadFile(session.Paths.MetadataPath)
	if err != nil {
		t.Fatalf("read metadata after reset: %v", err)
	}
	if !reflect.DeepEqual(metadataAfter, metadataBefore) {
		t.Fatal("metadata changed across conversation reset")
	}

	result, err := session.Run(context.Background(), falken.RunRequest{Prompt: "second"})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if result.FinalOutput != "second answer" {
		t.Fatalf("second result = %+v, want second answer", result)
	}
	if len(llm.requests) != 2 {
		t.Fatalf("LLM request count = %d, want one main call for both runs", len(llm.requests))
	}
	if got := publicMessageRoles(llm.requests[1].Messages[:len(llm.requests[1].Messages)-1]); !reflect.DeepEqual(got, []falken.Role{falken.RoleSystem, falken.RoleUser}) {
		t.Fatalf("second request roles = %v, want fresh system/user after reset", got)
	}
	if len(llm.requests[1].Messages) > 1 && llm.requests[1].Messages[1].Content != "second" {
		t.Fatalf("second request user prompt = %+v, want second only", llm.requests[1].Messages)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestSessionE2E_PlanModeHappyPath(t *testing.T) {
	setStateHomeEnv(t)

	session, err := falken.New(falken.Config{
		WorkspaceDir:     publicTempWorkspace(t),
		ExecutionDetails: falken.ExecutionConfig{Mode: falken.ExecutionModeLocal},
		LLM: &publicFakeLLM{responses: []falken.CompletionResponse{
			{
				AssistantText: "normal run after planning",
				FinishReason:  falken.FinishReasonStop,
			},
			{
				ToolCalls: []falken.ToolCall{{
					ID:        "call-submit",
					Name:      "submit_plan_implementation",
					Arguments: json.RawMessage(`{"summary":"no implementation changes","verification_summary":"not needed"}`),
				}},
				FinishReason: falken.FinishReasonToolCalls,
			},
			{
				AssistantText: "normal run after planning",
				FinishReason:  falken.FinishReasonStop,
			},
		}},
		VerificationReviewerLLM: &publicFakeLLM{responses: []falken.CompletionResponse{{
			ToolCalls: []falken.ToolCall{{
				ID:        "review-1",
				Name:      "record_command_evidence_review",
				Arguments: json.RawMessage(`{"verdict":"not_applicable","verification_performed":false,"confidence":"high","reason":"no workspace changes were made"}`),
			}},
			FinishReason: falken.FinishReasonToolCalls,
		}}},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if mode, err := session.CurrentMode(); err != nil || mode != "default" {
		t.Fatalf("initial mode = %q/%v, want default", mode, err)
	}
	if err := session.EnterPlanMode(); err != nil {
		t.Fatalf("EnterPlanMode: %v", err)
	}
	if mode, err := session.CurrentMode(); err != nil || mode != "plan" {
		t.Fatalf("plan mode = %q/%v, want plan", mode, err)
	}
	plan := "# Goal\nExercise public plan mode with a valid implementation plan.\n\n# Files\nNo workspace files are modified by this public API happy path.\n\n# Changes\n1. Enter plan mode.\n2. Store this plan through the public API.\n3. Exit plan mode after validation.\n\n# Verification\nRun the happy path test and confirm mode transitions."
	if err := session.WritePlan(plan); err != nil {
		t.Fatalf("WritePlan: %v", err)
	}
	if got, err := session.ReadPlan(); err != nil || got != plan {
		t.Fatalf("ReadPlan = %q/%v, want written plan", got, err)
	}
	if err := session.ExitPlanMode(); err != nil {
		t.Fatalf("ExitPlanMode: %v", err)
	}
	if mode, err := session.CurrentMode(); err != nil || mode != "default" {
		t.Fatalf("mode after exit = %q/%v, want default", mode, err)
	}

	result, err := session.Run(context.Background(), falken.RunRequest{Prompt: "continue"})
	if err != nil {
		t.Fatalf("Run after plan: %v", err)
	}
	if !result.Completed || result.FinalOutput != "normal run after planning" {
		t.Fatalf("result = %+v, want normal run after plan", result)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func e2eWasmModuleReturning(output string) []byte {
	body := make([]byte, 1, 1+len(output)*8+4)
	body[0] = 0x00
	for i, b := range []byte(output) {
		body = append(body, 0x20, 0x02)
		body = append(body, 0x41)
		body = append(body, e2eAppendSLEB32(int32(b))...)
		body = append(body, 0x3a, 0x00)
		body = append(body, e2eAppendULEB(e2eMustUint32Len(i))...)
	}
	body = append(body, 0x41)
	body = append(body, e2eAppendSLEB32(e2eMustInt32Len(len(output)))...)
	body = append(body, 0x0b)

	return e2eWasmModule(body)
}

func e2eWasmModule(functionBody []byte) []byte {
	out := make([]byte, 0, 128+len(functionBody))
	out = append(out, 0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00)
	out = append(out, e2eWasmSection(1, []byte{
		0x01,
		0x60,
		0x04, 0x7f, 0x7f, 0x7f, 0x7f,
		0x01, 0x7f,
	})...)
	out = append(out, e2eWasmSection(3, []byte{0x01, 0x00})...)
	out = append(out, e2eWasmSection(5, []byte{0x01, 0x00, 0x01})...)

	exports := make([]byte, 1, 32)
	exports[0] = 0x02
	exports = append(exports, e2eWasmExport("memory", 0x02, 0)...)
	exports = append(exports, e2eWasmExport("falken_tool_run", 0x00, 0)...)
	out = append(out, e2eWasmSection(7, exports)...)

	code := make([]byte, 1, len(functionBody)+8)
	code[0] = 0x01
	code = append(code, e2eAppendULEB(e2eMustUint32Len(len(functionBody)))...)
	code = append(code, functionBody...)
	out = append(out, e2eWasmSection(10, code)...)
	return out
}

func e2eWasmSection(id byte, payload []byte) []byte {
	out := make([]byte, 1, len(payload)+8)
	out[0] = id
	out = append(out, e2eAppendULEB(e2eMustUint32Len(len(payload)))...)
	out = append(out, payload...)
	return out
}

func e2eWasmExport(name string, kind byte, index uint32) []byte {
	out := e2eAppendULEB(e2eMustUint32Len(len(name)))
	out = append(out, []byte(name)...)
	out = append(out, kind)
	out = append(out, e2eAppendULEB(index)...)
	return out
}

func e2eAppendULEB(value uint32) []byte {
	var out []byte
	for {
		b := byte(value & 0x7f)
		value >>= 7
		if value != 0 {
			b |= 0x80
		}
		out = append(out, b)
		if value == 0 {
			return out
		}
	}
}

func e2eAppendSLEB32(value int32) []byte {
	var out []byte
	for {
		b := byte(value & 0x7f)
		signBitSet := b&0x40 != 0
		value >>= 7
		done := (value == 0 && !signBitSet) || (value == -1 && signBitSet)
		if !done {
			b |= 0x80
		}
		out = append(out, b)
		if done {
			return out
		}
	}
}

func e2eMustUint32Len(length int) uint32 {
	const maxUint32Value = uint64(1<<32 - 1)
	if length < 0 || uint64(length) > maxUint32Value {
		panic("test wasm fixture length exceeds uint32")
	}
	// #nosec G115 -- bounds check above guarantees length fits uint32.
	return uint32(length)
}

func e2eMustInt32Len(length int) int32 {
	const maxInt32Value = uint64(1<<31 - 1)
	if length < 0 || uint64(length) > maxInt32Value {
		panic("test wasm fixture length exceeds int32")
	}
	// #nosec G115 -- bounds check above guarantees length fits int32.
	return int32(length)
}
