package schema

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

type llmSchema struct {
}

var _ SchemaResolvers = &llmSchema{}

func (s llmSchema) Install(srv *dagql.Server) {
	dagql.Fields[*core.Query]{
		dagql.Func("llm", s.llm).
			WithInput(dagql.PerSessionInput).
			Experimental("LLM support is not yet stabilized").
			Doc(`Initialize a Large Language Model (LLM)`).
			Args(
				dagql.Arg("model").Doc("Model to use"),
			),
	}.Install(srv)
	dagql.Fields[*core.LLM]{
		dagql.Func("model", s.model).
			Doc("return the model used by the llm"),
		dagql.Func("provider", s.provider).
			Doc("return the provider used by the llm"),
		dagql.Func("history", s.history).
			Doc("return the llm message history"),
		dagql.Func("serializeHistory", s.serializeHistory).
			Doc("return the message history serialized as text, suitable for LLM consumption (e.g. for summarization)"),
		dagql.Func("historyJSON", s.historyJSON).
			View(AllVersion).
			Doc("return the raw llm message history as json"),
		dagql.Func("historyJSON", s.historyJSONString).
			View(BeforeVersion("v0.18.4")).
			Doc("return the raw llm message history as json"),
		dagql.Func("withoutMessageHistory", s.withoutMessageHistory).
			Doc("Clear the message history, leaving only the system prompts"),
		dagql.Func("withoutSystemPrompts", s.withoutSystemPrompts).
			Doc("Clear the system prompts, leaving only the default system prompt"),
		dagql.Func("lastReply", s.lastReply).
			Doc("return the last llm reply from the history"),
		dagql.Func("withWorkspace", s.withWorkspace).
			Doc("Bind the LLM to a workspace, exposing its modules as tools exactly as the Dagger CLI would serve them for that workspace.").
			Args(
				dagql.Arg("workspace").Doc("The workspace to work in."),
			),
		dagql.Func("workspace", s.workspace).
			Doc("Return the workspace the LLM is bound to."),
		dagql.Func("withModel", s.withModel).
			Doc("swap out the llm model").
			Args(
				dagql.Arg("model").Doc("The model to use"),
			),
		dagql.Func("withPrompt", s.withPrompt).
			Doc("append a prompt to the llm context").
			Args(
				dagql.Arg("prompt").Doc("The prompt to send"),
			),
		dagql.Func("__mcp", func(ctx context.Context, self *core.LLM, _ struct{}) (dagql.Nullable[core.Void], error) {
			currentSrv, err := core.CurrentDagqlServer(ctx)
			if err != nil {
				return dagql.Null[core.Void](), err
			}
			return dagql.Null[core.Void](), self.MCP(ctx, currentSrv)
		}).
			Doc("instantiates an mcp server"),
		dagql.Func("withPromptFile", s.withPromptFile).
			Doc("append the contents of a file to the llm context").
			Args(
				dagql.Arg("file").Doc("The file to read the prompt from"),
			),
		dagql.Func("withSystemPrompt", s.withSystemPrompt).
			Doc("Add a system prompt to the LLM's environment").
			Args(
				dagql.Arg("prompt").Doc("The system prompt to send"),
			),
		dagql.Func("withResponse", s.withResponse).
			Doc("Append an assistant response to the message history").
			Args(
				dagql.Arg("content").Doc("The response content"),
				dagql.Arg("inputTokens").Doc("Uncached input tokens sent"),
				dagql.Arg("outputTokens").Doc("Tokens received from the model, including text and tool calls"),
				dagql.Arg("cachedTokenReads").Doc("Cached input tokens read"),
				dagql.Arg("cachedTokenWrites").Doc("Cached input tokens written"),
				dagql.Arg("totalTokens").Doc("Total tokens consumed by this response"),
			),
		dagql.Func("withToolCall", s.withToolCall).
			Doc("Append a tool call to the last assistant message").
			Args(
				dagql.Arg("call").Doc("The unique ID for this tool call"),
				dagql.Arg("tool").Doc("The name of the tool to call"),
				dagql.Arg("arguments").Doc("The arguments to pass to the tool"),
			),
		dagql.Func("withToolResponse", s.withToolResponse).
			Doc("Append a tool response to the message history").
			Args(
				dagql.Arg("call").Doc("The ID of the tool call this is responding to"),
				dagql.Arg("content").Doc("The response content from the tool"),
				dagql.Arg("errored").Doc("Whether the tool call resulted in an error"),
			),
		dagql.Func("withTools", s.withTools).
			Doc("Expose an object's methods as tools. Every eligible method of the bound object becomes a tool; a tool that returns this object's own type replaces it as the new state. Repeatable to bind several objects.").
			Args(
				// @expectedType(Node) lets a statically typed caller (e.g. Dang) pass
				// any object where this ID! is wanted, since every object implements
				// the universal Node interface; the value is conveyed as its id.
				dagql.Arg("object").Doc("The object whose methods become tools.").
					Directive(dagql.ExpectedTypeDirective("Node")),
				dagql.Arg("except").Doc("Method names to exclude from the toolset (e.g. constructors, entrypoints)."),
			),
		dagql.Func("withMaxTokens", s.withMaxTokens).
			Doc("Set the maximum number of output tokens the model may generate per API call").
			Args(
				dagql.Arg("tokens").Doc("The maximum number of output tokens (0 to use provider defaults)"),
			),
		dagql.Func("withoutDefaultSystemPrompt", s.withoutDefaultSystemPrompt).
			Doc("Disable the default system prompt"),
		dagql.Func("withMCPServer", s.withMCPServer).
			Doc("Add an external MCP server to the LLM").
			Args(
				dagql.Arg("name").Doc("The name of the MCP server"),
				dagql.Arg("service").Doc("The MCP service to run and communicate with over stdio"),
			),
		dagql.NodeFunc("sync", func(ctx context.Context, self dagql.ObjectResult[*core.LLM], _ struct{}) (res dagql.ID[*core.LLM], _ error) {
			id, err := self.ID()
			if err != nil {
				return res, err
			}
			return dagql.NewID[*core.LLM](id), nil
		}).
			Doc("synchronize LLM state"),
		dagql.NodeFunc("replay", s.replay).
			WithInput(dagql.PerCallInput).
			Doc("Re-emit telemetry spans for the full message history, allowing the TUI to display a loaded conversation"),
		dagql.NodeFunc("loop", s.loop).
			Doc("Submit the queued prompt, evaluate any tool calls, queue their results, and keep going until the model ends its turn").
			Args(
				dagql.Arg("maxAPICalls").Doc("Cap the number of API calls"),
			),
		dagql.Func("hasPrompt", s.hasPrompt).
			Doc("Indicates whether there are any queued prompts or tool results to send to the model"),
		dagql.NodeFunc("step", s.step).
			Doc("Submit the queued prompt or tool call results, evaluate any tool calls, and queue their results"),
		dagql.Func("attempt", s.attempt).
			Doc("create a branch in the LLM's history"),
		dagql.Func("tools", s.tools).
			Doc("print documentation for available tools"),
		dagql.Func("tokenUsage", s.tokenUsage).
			Doc("returns the token usage of the current state"),
	}.Install(srv)
	dagql.Fields[*core.LLMTokenUsage]{}.Install(srv)
	dagql.Fields[*core.LLMMessage]{}.Install(srv)
	dagql.Fields[*core.LLMContentBlock]{}.Install(srv)
	dagql.Fields[*core.LLMToolCall]{}.Install(srv)
	core.LLMMessageRoles.Install(srv)
	core.LLMContentBlockKinds.Install(srv)
	dagql.MustInputSpec(core.LLMContentBlockInput{}).Install(srv)
}

func (s *llmSchema) withWorkspace(ctx context.Context, llm *core.LLM, args struct {
	Workspace dagql.ID[*core.Workspace]
}) (*core.LLM, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	ws, err := args.Workspace.Load(ctx, srv)
	if err != nil {
		return nil, err
	}
	return llm.WithWorkspace(ws), nil
}

func (s *llmSchema) workspace(ctx context.Context, llm *core.LLM, args struct{}) (res dagql.ObjectResult[*core.Workspace], _ error) {
	ws := llm.Workspace()
	if ws.Self() == nil {
		// The LLM binds the current workspace by default, but a context with no
		// current workspace (e.g. `dagger shell --model` run outside a workspace)
		// leaves it unbound. Return an error rather than a zero-value Workspace!,
		// which nil-derefs in the Workspace field resolvers and crashes the engine.
		return res, fmt.Errorf("no workspace is bound to this LLM (no current workspace in this context)")
	}
	return ws, nil
}

func (s *llmSchema) model(ctx context.Context, llm *core.LLM, args struct{}) (string, error) {
	ep, err := llm.Endpoint(ctx)
	if err != nil {
		return "", err
	}
	return ep.Model, nil
}

func (s *llmSchema) provider(ctx context.Context, llm *core.LLM, args struct{}) (string, error) {
	ep, err := llm.Endpoint(ctx)
	if err != nil {
		return "", err
	}
	return string(ep.Provider), nil
}

func (s *llmSchema) lastReply(ctx context.Context, llm *core.LLM, args struct{}) (dagql.String, error) {
	reply, _ := llm.LastReply()
	return dagql.NewString(reply), nil
}

func (s *llmSchema) withModel(ctx context.Context, llm *core.LLM, args struct {
	Model string
}) (*core.LLM, error) {
	return llm.WithModel(args.Model), nil
}

func (s *llmSchema) withPrompt(ctx context.Context, llm *core.LLM, args struct {
	Prompt string
}) (*core.LLM, error) {
	return llm.WithPrompt(args.Prompt), nil
}

func (s *llmSchema) withSystemPrompt(ctx context.Context, llm *core.LLM, args struct {
	Prompt string
}) (*core.LLM, error) {
	return llm.WithSystemPrompt(args.Prompt), nil
}

func (s *llmSchema) withResponse(ctx context.Context, llm *core.LLM, args struct {
	Content           []dagql.InputObject[core.LLMContentBlockInput]
	InputTokens       int64 `default:"0"`
	OutputTokens      int64 `default:"0"`
	CachedTokenReads  int64 `default:"0"`
	CachedTokenWrites int64 `default:"0"`
	TotalTokens       int64 `default:"0"`
}) (*core.LLM, error) {
	blocks := make([]*core.LLMContentBlock, len(args.Content))
	for i, input := range args.Content {
		blocks[i] = input.Value.ToLLMContentBlock()
	}
	return llm.WithResponse(blocks, core.LLMTokenUsage{
		InputTokens:       args.InputTokens,
		OutputTokens:      args.OutputTokens,
		CachedTokenReads:  args.CachedTokenReads,
		CachedTokenWrites: args.CachedTokenWrites,
		TotalTokens:       args.TotalTokens,
	}), nil
}

func (s *llmSchema) withToolCall(ctx context.Context, llm *core.LLM, args struct {
	Call      string
	Tool      string
	Arguments core.JSON
}) (*core.LLM, error) {
	return llm.WithToolCall(args.Call, args.Tool, args.Arguments), nil
}

func (s *llmSchema) withToolResponse(ctx context.Context, llm *core.LLM, args struct {
	Call    string
	Content string
	Errored bool
}) (*core.LLM, error) {
	return llm.WithToolResponse(args.Call, args.Content, args.Errored), nil
}

func (s *llmSchema) withTools(ctx context.Context, llm *core.LLM, args struct {
	Object dagql.AnyID
	Except []string `default:"[]"`
}) (*core.LLM, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	id, err := args.Object.ID()
	if err != nil {
		return nil, err
	}
	obj, err := srv.Load(ctx, id)
	if err != nil {
		return nil, err
	}
	return llm.WithTools(obj, args.Except), nil
}

func (s *llmSchema) withMaxTokens(_ context.Context, llm *core.LLM, args struct {
	Tokens int
}) (*core.LLM, error) {
	return llm.WithMaxTokens(args.Tokens), nil
}

func (s *llmSchema) withoutDefaultSystemPrompt(ctx context.Context, llm *core.LLM, args struct{}) (*core.LLM, error) {
	return llm.WithoutDefaultSystemPrompt(), nil
}

func (s *llmSchema) withMCPServer(ctx context.Context, llm *core.LLM, args struct {
	Name    string
	Service core.ServiceID
}) (*core.LLM, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	svc, err := args.Service.Load(ctx, srv)
	if err != nil {
		return nil, err
	}
	return llm.WithMCPServer(args.Name, svc), nil
}

func (s *llmSchema) withPromptFile(ctx context.Context, llm *core.LLM, args struct {
	File core.FileID
}) (*core.LLM, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	file, err := args.File.Load(ctx, srv)
	if err != nil {
		return nil, err
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return nil, err
	}
	if err := cache.Evaluate(ctx, file); err != nil {
		return nil, err
	}
	prompt, err := file.Self().Contents(ctx, file, nil, nil)
	if err != nil {
		return nil, err
	}
	return llm.WithPrompt(string(prompt)), nil
}

func (s *llmSchema) loop(ctx context.Context, parent dagql.ObjectResult[*core.LLM], args struct {
	MaxAPICalls dagql.Optional[dagql.Int] `name:"maxAPICalls"`
}) (dagql.ObjectResult[*core.LLM], error) {
	return parent.Self().Loop(ctx, parent, int(args.MaxAPICalls.Value))
}

func (s *llmSchema) step(ctx context.Context, parent dagql.ObjectResult[*core.LLM], args struct{}) (id dagql.ID[*core.LLM], err error) {
	inst, err := parent.Self().Step(ctx, parent)
	if err != nil {
		return id, err
	}
	instID, err := inst.ID()
	if err != nil {
		return id, err
	}
	return dagql.NewID[*core.LLM](instID), nil
}

func (s *llmSchema) replay(ctx context.Context, parent dagql.ObjectResult[*core.LLM], _ struct{}) (res dagql.ID[*core.LLM], _ error) {
	parent.Self().Replay(ctx)
	id, err := parent.ID()
	if err != nil {
		return res, err
	}
	return dagql.NewID[*core.LLM](id), nil
}

func (s *llmSchema) hasPrompt(ctx context.Context, llm *core.LLM, args struct{}) (bool, error) {
	return llm.HasPrompt(), nil
}

func (s *llmSchema) attempt(_ context.Context, llm *core.LLM, _ struct {
	Number int
}) (*core.LLM, error) {
	// clone the LLM object, so we can branch off from here
	return llm.Clone(), nil
}

func (s *llmSchema) llm(ctx context.Context, parent *core.Query, args struct {
	Model dagql.Optional[dagql.String]
}) (*core.LLM, error) {
	var model string
	if args.Model.Valid {
		model = args.Model.Value.String()
	}
	return parent.NewLLM(ctx, model)
}

func (s *llmSchema) history(ctx context.Context, llm *core.LLM, _ struct{}) ([]string, error) {
	return llm.History(ctx)
}

func (s *llmSchema) serializeHistory(ctx context.Context, llm *core.LLM, _ struct{}) (string, error) {
	return llm.SerializeHistory(), nil
}

func (s *llmSchema) historyJSON(ctx context.Context, llm *core.LLM, _ struct{}) (core.JSON, error) {
	return llm.HistoryJSON(ctx)
}

func (s *llmSchema) historyJSONString(ctx context.Context, llm *core.LLM, _ struct{}) (string, error) {
	js, err := llm.HistoryJSON(ctx)
	if err != nil {
		return "", err
	}
	return js.String(), nil
}

func (s *llmSchema) tools(ctx context.Context, llm *core.LLM, _ struct{}) (string, error) {
	return llm.ToolsDoc(ctx)
}

func (s *llmSchema) tokenUsage(ctx context.Context, llm *core.LLM, _ struct{}) (*core.LLMTokenUsage, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	return llm.TokenUsage(ctx, srv)
}

func (s *llmSchema) withoutMessageHistory(ctx context.Context, llm *core.LLM, _ struct{}) (*core.LLM, error) {
	return llm.WithoutMessageHistory(), nil
}

func (s *llmSchema) withoutSystemPrompts(ctx context.Context, llm *core.LLM, _ struct{}) (*core.LLM, error) {
	return llm.WithoutSystemPrompts(), nil
}
