package falken

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

// StaticToolProvider returns a ToolProvider backed by a fixed set of native Go
// tools.
func StaticToolProvider(tools ...Tool) ToolProvider {
	return &staticToolProvider{tools: append([]Tool(nil), tools...)}
}

// ToolFunc adapts a descriptor and function into a native Go Tool.
func ToolFunc(descriptor ToolDescriptor, fn func(context.Context, ToolInvocation) (ToolExecutionResult, error)) Tool {
	return toolFunc{
		descriptor: cloneToolDescriptor(descriptor),
		fn:         fn,
	}
}

// toolFunc is an internal implementation of Tool wrapping a function.
type toolFunc struct {
	descriptor ToolDescriptor
	fn         func(context.Context, ToolInvocation) (ToolExecutionResult, error)
}

// Descriptor returns the static metadata for the tool.
func (t toolFunc) Descriptor() ToolDescriptor {
	return cloneToolDescriptor(t.descriptor)
}

// Execute runs the underlying tool function with the given invocation.
func (t toolFunc) Execute(ctx context.Context, invocation ToolInvocation) (ToolExecutionResult, error) {
	if t.fn == nil {
		return ToolExecutionResult{}, errors.New("tool function is nil")
	}
	return t.fn(ctx, cloneToolInvocation(invocation))
}

// staticToolProvider implements ToolProvider for a static slice of tools.
type staticToolProvider struct {
	mu          sync.Mutex
	tools       []Tool
	descriptors []ToolDescriptor
	toolsByName map[string]Tool
}

// Start validates the tools configured for the static provider.
func (p *staticToolProvider) Start(context.Context, ToolHost) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	descriptors, toolsByName, err := validateStaticTools(p.tools)
	if err != nil {
		return err
	}
	p.descriptors = descriptors
	p.toolsByName = toolsByName
	return nil
}

// Tools returns the combined descriptors of all static tools.
func (p *staticToolProvider) Tools(context.Context) ([]ToolDescriptor, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.toolsByName == nil {
		descriptors, toolsByName, err := validateStaticTools(p.tools)
		if err != nil {
			return nil, err
		}
		p.descriptors = descriptors
		p.toolsByName = toolsByName
	}
	return cloneToolDescriptors(p.descriptors), nil
}

// ExecuteTool dispatches the invocation to the specific static tool.
func (p *staticToolProvider) ExecuteTool(ctx context.Context, invocation ToolInvocation) (ToolExecutionResult, error) {
	tool, err := p.toolForName(invocation.Name)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	result, err := tool.Execute(ctx, cloneToolInvocation(invocation))
	if err != nil {
		return ToolExecutionResult{}, err
	}
	return cloneToolExecutionResult(result), nil
}

// toolForName locates a static tool by its descriptor name.
func (p *staticToolProvider) toolForName(name string) (Tool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.toolsByName == nil {
		descriptors, toolsByName, err := validateStaticTools(p.tools)
		if err != nil {
			return nil, err
		}
		p.descriptors = descriptors
		p.toolsByName = toolsByName
	}
	tool, ok := p.toolsByName[name]
	if !ok {
		return nil, fmt.Errorf("unknown static tool: %s", name)
	}
	return tool, nil
}

// Close is a no-op for staticToolProvider.
func (p *staticToolProvider) Close(context.Context) error {
	return nil
}

// validateStaticTools ensures a static tool set contains valid and unique tools.
func validateStaticTools(staticTools []Tool) ([]ToolDescriptor, map[string]Tool, error) {
	descriptors := make([]ToolDescriptor, 0, len(staticTools))
	toolsByName := make(map[string]Tool, len(staticTools))
	for _, tool := range staticTools {
		if tool == nil {
			return nil, nil, errors.New("static tool is nil")
		}
		descriptor := cloneToolDescriptor(tool.Descriptor())
		if err := ValidateToolDescriptor(descriptor); err != nil {
			return nil, nil, err
		}
		if _, exists := toolsByName[descriptor.Name]; exists {
			return nil, nil, fmt.Errorf("duplicate static tool name %q", descriptor.Name)
		}
		descriptors = append(descriptors, descriptor)
		toolsByName[descriptor.Name] = tool
	}
	return descriptors, toolsByName, nil
}

// cloneToolDescriptors deeply copies a slice of ToolDescriptors.
func cloneToolDescriptors(descriptors []ToolDescriptor) []ToolDescriptor {
	cloned := make([]ToolDescriptor, 0, len(descriptors))
	for _, descriptor := range descriptors {
		cloned = append(cloned, cloneToolDescriptor(descriptor))
	}
	return cloned
}

// cloneToolDescriptor deeply copies a single ToolDescriptor.
func cloneToolDescriptor(descriptor ToolDescriptor) ToolDescriptor {
	descriptor.Parameters = append(json.RawMessage(nil), descriptor.Parameters...)
	descriptor.Keywords = append([]string(nil), descriptor.Keywords...)
	return descriptor
}

// cloneToolInvocation deeply copies a ToolInvocation.
func cloneToolInvocation(invocation ToolInvocation) ToolInvocation {
	invocation.Arguments = append(json.RawMessage(nil), invocation.Arguments...)
	return invocation
}

// cloneToolExecutionResult deeply copies a ToolExecutionResult.
func cloneToolExecutionResult(result ToolExecutionResult) ToolExecutionResult {
	result.Payload = append(json.RawMessage(nil), result.Payload...)
	return result
}
