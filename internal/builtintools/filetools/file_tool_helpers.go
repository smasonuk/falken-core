package filetools

import (
	"context"
	"encoding/json"

	"github.com/smasonuk/falken-core/internal/agent"
	"github.com/smasonuk/falken-core/internal/builtintools/api"
	"github.com/smasonuk/falken-core/pkg/falken/workspacefiles"
)

type fileToolSpec[Req any, Res any] struct {
	descriptor api.Descriptor
	run        func(context.Context, workspacefiles.Operations, Req) (Res, error)
	success    func(Res) bool
	status     func(Res) string
	content    func(Res) string
}

func (s fileToolSpec[Req, Res]) Descriptor() api.Descriptor {
	return s.descriptor
}

func (s fileToolSpec[Req, Res]) Execute(
	ctx context.Context,
	host *api.Host,
	args json.RawMessage,
) (agent.ToolExecutionResult, error) {
	return executeFileTool(ctx, host, args, s.run, s.success, s.status, s.content)
}

func executeFileTool[Req any, Res any](
	ctx context.Context,
	host *api.Host,
	args json.RawMessage,
	run func(context.Context, workspacefiles.Operations, Req) (Res, error),
	success func(Res) bool,
	status func(Res) string,
	content func(Res) string,
) (agent.ToolExecutionResult, error) {
	var req Req
	if err := api.DecodeArgs(args, &req); err != nil {
		return api.Fail("invalid_arguments", err.Error()), nil
	}

	ops, err := host.RequireFileOps()
	if err != nil {
		return agent.ToolExecutionResult{}, err
	}

	result, err := run(ctx, ops, req)
	if err != nil {
		return agent.ToolExecutionResult{}, err
	}

	return api.ResultFromPayload(success(result), status(result), content(result), result)
}
