package schema

import (
	"context"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/iancoleman/strcase"
)

type llmSchema struct {
	srv *dagql.Server
}

var _ SchemaResolvers = &llmSchema{}

func (s llmSchema) Install(srv *dagql.Server) {
	dagql.Fields[*core.Query]{
		dagql.FuncWithCacheKey("llm", s.llm, dagql.CachePerSession).
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
		dagql.Func("withEnv", s.withEnv).
			Doc("allow the LLM to interact with an environment via MCP"),
		dagql.Func("env", s.env).
			Doc("return the LLM's current environment"),
		dagql.Func("withStaticTools", s.withStaticTools).
			Doc("Use a static set of tools for method calls, e.g. for MCP clients that do not support dynamic tool registration"),
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
			return dagql.Null[core.Void](), self.MCP(ctx, srv)
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
		dagql.Func("__withObject", s.withObject).
			Doc("Track an object by an arbitrary string tag, like Container#123, for the LLM to reference it by in arguments etc.").
			Args(
				dagql.Arg("tag").Doc("Arbitrary string, typically in TypeName#Number format"),
				dagql.Arg("object").Doc("Arbitrary object ID"),
			),
		dagql.Func("withoutDefaultSystemPrompt", s.withoutDefaultSystemPrompt).
			Doc("Disable the default system prompt"),
		dagql.Func("withBlockedFunction", s.withBlockedFunction).
			Doc("Return a new LLM with the specified function no longer exposed as a tool").
			Args(
				dagql.Arg("typeName").Doc("The type name whose function will be blocked"),
				dagql.Arg("function").Doc("The function to block", "Will be converted to lowerCamelCase if necessary."),
			),
		dagql.Func("withMCPServer", s.withMCPServer).
			Doc("Add an external MCP server to the LLM").
			Args(
				dagql.Arg("name").Doc("The name of the MCP server"),
				dagql.Arg("service").Doc("The MCP service to run and communicate with over stdio"),
			),
		dagql.NodeFunc("sync", func(ctx context.Context, self dagql.ObjectResult[*core.LLM], _ struct{}) (res dagql.ID[*core.LLM], _ error) {
			return dagql.NewID[*core.LLM](self.ID()), nil
		}).
			Doc("synchronize LLM state"),
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
		dagql.Func("bindResult", s.bindResult).
			Doc("returns the type of the current state"),
		dagql.Func("tokenUsage", s.tokenUsage).
			Doc("returns the token usage of the current state"),
	}.Install(srv)
	dagql.Fields[*core.LLMTokenUsage]{}.Install(srv)
	dagql.Fields[*core.LLMMessage]{}.Install(srv)
	dagql.Fields[*core.LLMToolCall]{}.Install(srv)
	core.LLMMessageRoles.Install(srv)
}

func (s *llmSchema) withEnv(ctx context.Context, llm *core.LLM, args struct {
	Env core.EnvID
}) (*core.LLM, error) {
	env, err := args.Env.Load(ctx, s.srv)
	if err != nil {
		return nil, err
	}
	return llm.WithEnv(env), nil
}

func (s *llmSchema) withStaticTools(ctx context.Context, llm *core.LLM, args struct{}) (*core.LLM, error) {
	return llm.WithStaticTools(), nil
}

func (s *llmSchema) env(ctx context.Context, llm *core.LLM, args struct{}) (res dagql.ObjectResult[*core.Env], _ error) {
	return llm.Env(), nil
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

func (s *llmSchema) lastReply(ctx context.Context, llm *core.LLM, args struct{}) (string, error) {
	reply, _ := llm.LastReply()
	// TODO: should we error if no last reply? (breaking)
	return reply, nil
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
	Content           string
	InputTokens       int64 `default:"0"`
	OutputTokens      int64 `default:"0"`
	CachedTokenReads  int64 `default:"0"`
	CachedTokenWrites int64 `default:"0"`
	TotalTokens       int64 `default:"0"`
}) (*core.LLM, error) {
	return llm.WithResponse(args.Content, core.LLMTokenUsage{
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

func (s *llmSchema) withObject(ctx context.Context, llm *core.LLM, args struct {
	Tag    string
	Object core.ID
}) (*core.LLM, error) {
	return llm.WithObject(args.Tag, args.Object), nil
}

func (s *llmSchema) withoutDefaultSystemPrompt(ctx context.Context, llm *core.LLM, args struct{}) (*core.LLM, error) {
	return llm.WithoutDefaultSystemPrompt(), nil
}

func (s *llmSchema) withBlockedFunction(ctx context.Context, llm *core.LLM, args struct {
	TypeName string
	Function string
}) (*core.LLM, error) {
	return llm.WithBlockedFunction(ctx,
		args.TypeName,
		// We're stringly typed, which sucks, but we can at least allow people to
		// refer to names in their locale (e.g. snake_case in Python.)
		strcase.ToLowerCamel(args.Function),
	)
}

func (s *llmSchema) withMCPServer(ctx context.Context, llm *core.LLM, args struct {
	Name    string
	Service core.ServiceID
}) (*core.LLM, error) {
	svc, err := args.Service.Load(ctx, s.srv)
	if err != nil {
		return nil, err
	}
	return llm.WithMCPServer(args.Name, svc), nil
}

func (s *llmSchema) withPromptFile(ctx context.Context, llm *core.LLM, args struct {
	File core.FileID
}) (*core.LLM, error) {
	file, err := args.File.Load(ctx, s.srv)
	if err != nil {
		return nil, err
	}
	return llm.WithPromptFile(ctx, file.Self())
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
	return dagql.NewID[*core.LLM](inst.ID()), err
}

func (s *llmSchema) hasPrompt(ctx context.Context, llm *core.LLM, args struct{}) (bool, error) {
	return llm.HasPrompt(), nil
}

func (s *llmSchema) attempt(_ context.Context, llm *core.LLM, _ struct {
	Number int
}) (*core.LLM, error) {
	// clone the LLM object, since it updates in-place when Sync is called, and we
	// want to branch off from here
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

func (s *llmSchema) bindResult(ctx context.Context, llm *core.LLM, args struct {
	Name string
}) (dagql.Nullable[*core.Binding], error) {
	return llm.BindResult(ctx, s.srv, args.Name)
}

func (s *llmSchema) tokenUsage(ctx context.Context, llm *core.LLM, _ struct{}) (*core.LLMTokenUsage, error) {
	return llm.TokenUsage(ctx, s.srv)
}

func (s *llmSchema) withoutMessageHistory(ctx context.Context, llm *core.LLM, _ struct{}) (*core.LLM, error) {
	return llm.WithoutMessageHistory(), nil
}

func (s *llmSchema) withoutSystemPrompts(ctx context.Context, llm *core.LLM, _ struct{}) (*core.LLM, error) {
	return llm.WithoutSystemPrompts(), nil
}
