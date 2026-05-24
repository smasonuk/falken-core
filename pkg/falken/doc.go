// Package falken exposes Falken's core session, agent, policy, file, and
// runtime contracts.
//
// For the common embedded-agent path, NewAgent creates and starts a local
// in-memory agent without requiring callers to configure sessions, state
// directories, Docker/runtime providers, plan routing, memory, or policy:
//
//	agent, err := falken.NewAgent(ctx, falken.AgentConfig{
//		LLM:          llm,
//		SystemPrompt: "Help with user account questions.",
//		Tools:        []falken.Tool{lookupUserTool},
//	})
//	if err != nil {
//		return err
//	}
//	defer agent.Close(ctx)
//
//	answer, err := agent.Run(ctx, "Summarize the account")
//
// NewAgent uses in-memory conversation state by default and does not use
// Docker, sandbox runtimes, or runtime providers. With ReadDirectory set, it
// exposes only read-only file built-ins rooted at that directory by default:
//
//	agent, err := falken.NewAgent(ctx, falken.AgentConfig{
//		LLM:           llm,
//		SystemPrompt:  "Answer only using the docs directory.",
//		ReadDirectory: "./docs",
//	})
//
// Use New and Session when you need the advanced assembly path: custom
// runtime providers, durable conversation state, project permissions,
// lifecycle hooks, plan routing, or the full built-in tool set. A host creates
// a Session with New, starts it once, runs prompts, and closes it when finished:
//
//	session, err := falken.New(falken.Config{
//		WorkspaceDir: ".",
//		LLM:          llm,
//		Runtime:      runtimeProvider,
//	})
//	if err != nil {
//		return err
//	}
//	if err := session.Start(); err != nil {
//		return err
//	}
//	defer session.Close()
//
//	result, err := session.Run(ctx, falken.RunRequest{Prompt: "Update the tests"})
//
// Config requires a workspace and LLM. Runtime is required for sandbox
// execution; no runtime provider is accepted only when Execution.Mode is
// ExecutionModeLocal, which is intended for development and tests. Optional
// adapters such as Docker runtimes, OpenAI/LangChain LLMs, and Wasm tools live
// outside core.
//
// Built-in tools cover command execution, managed file reads and mutations, and
// plan state. Hosts can add native tools with ToolProvider, ToolFunc, and
// StaticToolProvider. Tool descriptors are validated with ValidateToolDescriptor
// before exposure to the model. ToolSafety controls Plan-mode exposure and the
// capabilities available through scoped ToolHost values.
//
// ToolProvider.Start receives a metadata-only host for paths and events. It is
// intentionally capability-scoped with no shell, file, or state access so a
// provider cannot retain it and bypass per-tool safety. Providers that need
// host services during execution should implement ScopedToolProvider; core then
// passes ExecuteToolWithHost a call-scoped ToolHost whose command, file, state,
// and event access matches the invoked tool descriptor. Non-scoped providers are
// supported for compatibility, but they do not receive per-run event sinks or
// per-tool host capabilities.
//
// ToolHost is the policy-gated bridge for external tools. ExecuteCommand uses
// the session command executor and policy engine. CheckFileAccess resolves paths
// safely inside the workspace before policy evaluation. GetState and SetState
// store hashed state under a host-derived namespace, so tools do not choose
// another provider's state namespace. Emit sends events through the session and,
// for scoped execution, the current run.
//
// Wasm tools are provided by falken-extra/wasmtools:
//
//	ToolProviders: []falken.ToolProvider{
//		wasmtools.NewProvider(wasmtools.Config{
//			Roots:   []string{"./tools"},
//			Runtime: wasmtools.NewWazeroRuntime(),
//		}),
//	}
//
// Core no longer discovers ToolRoots or PluginRoots and does not instantiate a
// Wasm runtime. Wasm manifest parsing, declared-permission checks, and wazero
// execution belong to falken-extra/wasmtools.
//
// HookProvider currently supports only session lifecycle hooks:
// HookSessionStart runs after successful startup and can fail Session.Start;
// HookSessionClose runs during Close and its errors are joined with cleanup
// errors while cleanup continues. Hooks are not exposed to the LLM tool list.
//
// Events are stable host-facing notifications for assistant text, tool calls,
// tool results, command output chunks, run completion/failure, network requests,
// and best-effort thought messages. StreamingLLM can emit incremental assistant
// text while still returning one final CompletionResponse for history.
//
// Deprecated constructors such as NewSession and NewSessionWithConfig remain
// for compatibility and tests. New with Config is the public assembly path.
// Hosts should not import internal packages; public contracts in this package
// are the supported extension surface.
package falken
