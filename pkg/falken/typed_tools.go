package falken

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// ToolOption customizes a tool created by Func.
type ToolOption func(*typedToolConfig)

type typedToolConfig struct {
	category    string
	keywords    []string
	safety      ToolSafety
	schema      *Schema
	alwaysLoad  bool
	defaultLoad bool
}

// WithToolCategory sets the descriptor category.
func WithToolCategory(category string) ToolOption {
	return func(config *typedToolConfig) {
		config.category = category
	}
}

// WithToolKeywords sets descriptor search keywords.
func WithToolKeywords(keywords ...string) ToolOption {
	return func(config *typedToolConfig) {
		config.keywords = append([]string(nil), keywords...)
	}
}

// WithToolSafety sets descriptor safety metadata.
func WithToolSafety(safety ToolSafety) ToolOption {
	return func(config *typedToolConfig) {
		config.safety = safety
	}
}

// WithToolSchema overrides the schema derived from Args.
func WithToolSchema(schema Schema) ToolOption {
	return func(config *typedToolConfig) {
		cloned := Schema{raw: schema.JSON()}
		config.schema = &cloned
	}
}

// WithToolAlwaysLoad sets the descriptor always-load flag.
func WithToolAlwaysLoad(value bool) ToolOption {
	return func(config *typedToolConfig) {
		config.alwaysLoad = value
	}
}

// WithToolDefaultLoad sets the descriptor default-load flag.
func WithToolDefaultLoad(value bool) ToolOption {
	return func(config *typedToolConfig) {
		config.defaultLoad = value
	}
}

// Func creates a typed Go tool, deriving its argument schema from Args unless
// WithToolSchema is supplied.
func Func[Args any, Result any](
	name string,
	description string,
	fn func(context.Context, Args) (Result, error),
	opts ...ToolOption,
) Tool {
	var config typedToolConfig
	for _, opt := range opts {
		if opt != nil {
			opt(&config)
		}
	}

	var schema Schema
	if config.schema != nil {
		schema = *config.schema
	} else if derived, err := SchemaFor[Args](); err == nil {
		schema = derived
	}

	return JSONTool[Args, Result](ToolDescriptor{
		Name:        name,
		Description: description,
		Parameters:  schema.JSON(),
		Category:    config.category,
		Keywords:    append([]string(nil), config.keywords...),
		AlwaysLoad:  config.alwaysLoad,
		DefaultLoad: config.defaultLoad,
		Safety:      config.safety,
	}, fn)
}

// JSONTool adapts a typed function and descriptor into a Tool.
func JSONTool[Args any, Result any](
	descriptor ToolDescriptor,
	fn func(context.Context, Args) (Result, error),
) Tool {
	return typedJSONTool[Args, Result]{
		descriptor: cloneToolDescriptor(descriptor),
		fn:         fn,
	}
}

type typedJSONTool[Args any, Result any] struct {
	descriptor ToolDescriptor
	fn         func(context.Context, Args) (Result, error)
}

func (t typedJSONTool[Args, Result]) Descriptor() ToolDescriptor {
	return cloneToolDescriptor(t.descriptor)
}

func (t typedJSONTool[Args, Result]) Execute(ctx context.Context, invocation ToolInvocation) (ToolExecutionResult, error) {
	var args Args
	if err := decodeToolArguments(invocation.Arguments, &args); err != nil {
		return failedToolResult("invalid_arguments", err.Error()), nil
	}
	if t.fn == nil {
		return failedToolResult("error", "tool function is nil"), nil
	}

	result, err := t.fn(ctx, args)
	if err != nil {
		return failedToolResult("error", err.Error()), nil
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return failedToolResult("encode_error", err.Error()), nil
	}
	return ToolExecutionResult{
		Success: true,
		Status:  "ok",
		Content: toolResultContent(result, payload),
		Payload: payload,
	}, nil
}

func decodeToolArguments(arguments json.RawMessage, target any) error {
	trimmed := strings.TrimSpace(string(arguments))
	switch trimmed {
	case "":
		arguments = json.RawMessage(`{}`)
	case "null":
		return errors.New("arguments must be a JSON object, not null")
	}

	dec := json.NewDecoder(bytes.NewReader(arguments))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("arguments must contain exactly one JSON value")
	}
	return nil
}

func failedToolResult(status, message string) ToolExecutionResult {
	payload, err := json.Marshal(map[string]any{
		"success": false,
		"status":  status,
		"error":   message,
	})
	if err != nil {
		payload = json.RawMessage(`{"success":false,"status":"encode_error","error":"encode tool result"}`)
	}
	return ToolExecutionResult{
		Success: false,
		Status:  status,
		Content: message,
		Payload: payload,
		Error:   message,
	}
}

func toolResultContent(result any, payload []byte) string {
	switch value := result.(type) {
	case string:
		return value
	case fmt.Stringer:
		return value.String()
	default:
		return string(payload)
	}
}
