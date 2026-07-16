package schema

import (
	"context"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/iancoleman/strcase"
)

type llmSchema struct {
}

var _ SchemaResolvers = &llmSchema{}

func (s llmSchema) Install(srv *dagql.Server) {
	dagql.Fields[*core.Query]{
		dagql.Func("llm", s.llm).
			WithInput(dagql.PerSessionInput).
			Experimental("LLM support is not yet stabilized").
			Doc(`Initialize a new LLM conversation.`).
			Args(
				dagql.Arg("model").Doc(
					`The model to converse with, e.g. "claude-sonnet-4-5" or "gpt-5.4". Defaults to the configured default model.`),
				dagql.Arg("provider").Doc(
					`The provider serving the model, e.g. "openai". Overrides the provider otherwise inferred from the model name — useful when the name matches no known pattern (e.g. a fine-tune), or matches the wrong one.`).
					View(AfterVersion("v1.0.0-0")),
				dagql.Arg("maxAPICalls").Doc("Cap the number of API calls for this LLM").
					View(BeforeVersion("v1.0.0-0")),
			),
	}.Install(srv)
	dagql.Fields[*core.LLM]{
		dagql.Func("model", s.model).
			Doc("The model the conversation is running against, after resolving any configured default."),
		dagql.Func("provider", s.provider).
			Doc(`The provider serving the model, e.g. "anthropic", "openai", "google", or "local".`),
		dagql.Func("contextWindow", s.contextWindow).
			View(AfterVersion("v1.0.0-0")).
			Doc("The model's total context window in tokens, or null if unknown (e.g. a local or uncatalogued model)."),
		// history and historyJSON are superseded in v1 by messages (structured)
		// and transcript (plain text), but remain visible to pre-v1 module views:
		// the record/replay test machinery still runs pre-v1 modules that dump
		// conversations with historyJSON.
		dagql.Func("history", s.history).
			View(BeforeVersion("v1.0.0-0")).
			Doc("return the llm message history"),
		dagql.Func("historyJSON", s.historyJSON).
			View(BeforeVersion("v1.0.0-0")).
			Doc("return the raw llm message history as json"),
		dagql.Func("historyJSON", s.historyJSONString).
			View(BeforeVersion("v0.18.4")).
			Doc("return the raw llm message history as json"),
		dagql.Func("messages", s.messages).
			View(AfterVersion("v1.0.0-0")).
			Doc("The full message history, as structured messages."),
		dagql.Func("transcript", s.transcript).
			View(AfterVersion("v1.0.0-0")).
			Doc("The message history rendered as a plain-text transcript, suitable for feeding back to an LLM (e.g. for summarization)."),
		dagql.Func("withoutMessageHistory", s.withoutMessageHistory).
			Doc("Clear the message history, keeping only the system prompts."),
		dagql.Func("withoutSystemPrompts", s.withoutSystemPrompts).
			Doc("Clear the user-added system prompts, keeping only the default system prompt."),
		dagql.Func("lastReply", s.lastReply).
			Doc("The text of the model's most recent reply."),
		dagql.Func("withEnv", s.withEnv).
			Doc("allow the LLM to interact with an environment via MCP"),
		dagql.Func("env", s.env).
			Doc("return the LLM's current environment"),
		dagql.Func("withStaticTools", s.withStaticTools).
			Doc("Use a static set of tools for method calls, e.g. for MCP clients that do not support dynamic tool registration"),
		dagql.Func("withModel", s.withModel).
			Doc("Change the model for the rest of the conversation. The message history is preserved; the new model takes effect on the next step.").
			Args(
				dagql.Arg("model").Doc(`The model to use, e.g. "claude-sonnet-4-5" or "gpt-5.4".`),
				dagql.Arg("provider").Doc(
					`The provider serving the model, e.g. "openai". Overrides the provider otherwise inferred from the model name — useful when the name matches no known pattern (e.g. a fine-tune), or matches the wrong one.`).
					View(AfterVersion("v1.0.0-0")),
			),
		dagql.Func("withPrompt", s.withPrompt).
			Doc("Queue a user prompt, to be sent to the model on the next step or loop.").
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
			Doc("Queue a file's contents as a user prompt, like withPrompt.").
			Args(
				dagql.Arg("file").Doc("The file to read the prompt from"),
			),
		dagql.Func("withSystemPrompt", s.withSystemPrompt).
			Doc("Add a system prompt, instructing the model across the whole conversation.").
			Args(
				dagql.Arg("prompt").Doc("The system prompt to send"),
			),
		dagql.Func("withResponse", s.withResponse).
			View(AfterVersion("v1.0.0-0")).
			Doc("Append an assistant response to the message history without calling the model, e.g. to reconstruct a conversation from another source.").
			Args(
				dagql.Arg("content").Doc("The response content"),
				dagql.Arg("inputTokens").Doc("Uncached input tokens sent"),
				dagql.Arg("outputTokens").Doc("Tokens received from the model, including text and tool calls"),
				dagql.Arg("cachedTokenReads").Doc("Cached input tokens read"),
				dagql.Arg("cachedTokenWrites").Doc("Cached input tokens written"),
				dagql.Arg("totalTokens").Doc("Total tokens consumed by this response"),
			),
		dagql.Func("withToolCall", s.withToolCall).
			View(AfterVersion("v1.0.0-0")).
			Doc("Append a tool call to the last assistant message, e.g. to reconstruct a conversation from another source.").
			Args(
				dagql.Arg("callId").Doc("The unique ID for this tool call"),
				dagql.Arg("toolName").Doc("The name of the tool to call"),
				dagql.Arg("arguments").Doc("The arguments to pass to the tool, JSON-encoded"),
			),
		dagql.Func("withToolResult", s.withToolResult).
			View(AfterVersion("v1.0.0-0")).
			Doc("Append the result of a tool call to the message history.").
			Args(
				dagql.Arg("callId").Doc("The ID of the tool call this result responds to"),
				dagql.Arg("content").Doc("The content returned by the tool"),
				dagql.Arg("errored").Doc("Whether the tool call resulted in an error"),
			),
		dagql.Func("withObject", s.withObject).
			View(AfterVersion("v1.0.0-0")).
			Doc("Track an object so the LLM can reference it in subsequent tool calls.").
			Args(
				dagql.Arg("tag").Doc("Arbitrary string tag for the object, typically in TypeName#Number format"),
				dagql.Arg("object").Doc("The object to track, as a generic ID"),
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
			id, err := self.ID()
			if err != nil {
				return res, err
			}
			return dagql.NewID[*core.LLM](id), nil
		}).
			Doc("Force evaluation of the conversation's pending operations (prompts, steps, loops) in the engine."),
		dagql.NodeFunc("portableID", func(ctx context.Context, self dagql.ObjectResult[*core.LLM], _ struct{}) (dagql.AnyID, error) {
			id, err := self.RecipeID(ctx)
			if err != nil {
				return dagql.AnyID{}, err
			}
			return dagql.NewAnyID(id), nil
		}).
			View(AfterVersion("v1.0.0-0")).
			DoNotCache("An ID describes the current attached result and must not be served from cache.").
			Doc("A portable, self-contained ID for the conversation that node() can resolve in any session. " +
				"Unlike id, which may return an engine-local runtime handle valid only within the current session, " +
				"this returns the recipe form suitable for persisting and later restoring the conversation."),
		dagql.NodeFunc("replay", s.replay).
			View(AfterVersion("v1.0.0-0")).
			WithInput(dagql.PerCallInput).
			Doc("Re-emit telemetry spans for the full message history, so a loaded conversation displays in the TUI."),
		dagql.NodeFunc("loop", s.loop).
			Doc("Send the queued prompt and step the model against the available tools, until it ends its turn: a reply with no tool calls and nothing left queued.").
			Args(
				dagql.Arg("maxSteps").Doc("Cap the number of steps. The loop fails if the cap is reached before the model ends its turn.").
					View(AfterVersion("v1.0.0-0")),
				dagql.Arg("maxTokens").Doc("Cap the model's output tokens on each step. Defaults to the model's maximum.").
					View(AfterVersion("v1.0.0-0")),
			),
		dagql.NodeFunc("step", s.step).
			Doc("Advance the conversation by a single step: send the queued prompt or tool results to the model, evaluate any tool calls it makes, and queue their results. Use loop to step until the model ends its turn.").
			Args(
				dagql.Arg("maxTokens").Doc("Cap the model's output tokens for this step. Defaults to the model's maximum.").
					View(AfterVersion("v1.0.0-0")),
			),
		dagql.Func("hasPending", s.hasPending).
			Doc("Report whether anything is queued to send to the model: an unsent prompt or unevaluated tool results. When true, another step will do work; when false, the turn is complete."),
		dagql.Func("fork", s.fork).
			View(AfterVersion("v1.0.0-0")).
			Doc("Fork the conversation, so that otherwise-identical follow-ups evaluate independently instead of deduplicating to a single cached result.").
			Args(
				dagql.Arg("label").Doc(`A label distinguishing this fork from its siblings, e.g. "attempt-2" when retrying a flaky evaluation.`),
			),
		// attempt is superseded in v1 by fork, but remains visible to pre-v1
		// module views (e.g. the evaluator module).
		dagql.Func("attempt", s.attempt).
			View(BeforeVersion("v1.0.0-0")).
			Doc("create a branch in the LLM's history"),
		dagql.Func("tools", s.tools).
			Doc("Render documentation for the tools currently exposed to the model."),
		dagql.Func("bindResult", s.bindResult).
			Doc("returns the type of the current state"),
		dagql.Func("tokenUsage", s.tokenUsage).
			Doc("The cumulative token usage, summed across every API call in the conversation."),
	}.Install(srv)
	dagql.Fields[*core.LLMTokenUsage]{}.Install(srv)
	// The content-block message model is only visible to v1+ module views;
	// installing the classes with a view gate also gates their generated
	// ID/load fields and Env/Binding extensions.
	srv.InstallObject(dagql.NewClass[*core.LLMMessage](srv).View(AfterVersion("v1.0.0-0")))
	srv.InstallObject(dagql.NewClass[*core.LLMContentBlock](srv).View(AfterVersion("v1.0.0-0")))
	dagql.Fields[*core.LLMMessage]{}.Install(srv)
	dagql.Fields[*core.LLMContentBlock]{}.Install(srv)
	core.LLMMessageRoles.Install(srv, AfterVersion("v1.0.0-0"))
	core.LLMContentBlockKinds.Install(srv, AfterVersion("v1.0.0-0"))
	dagql.MustInputSpec(core.LLMContentBlockInput{}).Install(srv, AfterVersion("v1.0.0-0"))
}

func (s *llmSchema) withEnv(ctx context.Context, llm *core.LLM, args struct {
	Env core.EnvID
}) (*core.LLM, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	env, err := args.Env.Load(ctx, srv)
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

func (s *llmSchema) contextWindow(ctx context.Context, llm *core.LLM, args struct{}) (dagql.Nullable[dagql.Int], error) {
	none := dagql.Null[dagql.Int]()
	ep, err := llm.Endpoint(ctx)
	if err != nil {
		return none, err
	}
	if ep.ContextWindow <= 0 {
		return none, nil
	}
	return dagql.NonNull(dagql.NewInt(int(ep.ContextWindow))), nil
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
	Model    string
	Provider dagql.Optional[dagql.String]
}) (*core.LLM, error) {
	return llm.WithModel(args.Model, args.Provider.Value.String()), nil
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
	CallID    string `name:"callId"`
	ToolName  string
	Arguments core.JSON
}) (*core.LLM, error) {
	return llm.WithToolCall(args.CallID, args.ToolName, args.Arguments), nil
}

func (s *llmSchema) withToolResult(ctx context.Context, llm *core.LLM, args struct {
	CallID  string `name:"callId"`
	Content string
	Errored bool
}) (*core.LLM, error) {
	return llm.WithToolResult(args.CallID, args.Content, args.Errored), nil
}

func (s *llmSchema) withObject(ctx context.Context, llm *core.LLM, args struct {
	Tag    string
	Object dagql.AnyID
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
	MaxSteps  dagql.Optional[dagql.Int] `name:"maxSteps"`
	MaxTokens dagql.Optional[dagql.Int] `name:"maxTokens"`
}) (dagql.ObjectResult[*core.LLM], error) {
	return parent.Self().Loop(ctx, parent, int(args.MaxSteps.Value), int(args.MaxTokens.Value))
}

func (s *llmSchema) step(ctx context.Context, parent dagql.ObjectResult[*core.LLM], args struct {
	MaxTokens dagql.Optional[dagql.Int] `name:"maxTokens"`
}) (dagql.ObjectResult[*core.LLM], error) {
	return parent.Self().Step(ctx, parent, int(args.MaxTokens.Value))
}

func (s *llmSchema) replay(ctx context.Context, parent dagql.ObjectResult[*core.LLM], _ struct{}) (res dagql.ID[*core.LLM], _ error) {
	parent.Self().Replay(ctx)
	id, err := parent.ID()
	if err != nil {
		return res, err
	}
	return dagql.NewID[*core.LLM](id), nil
}

func (s *llmSchema) hasPending(ctx context.Context, llm *core.LLM, args struct{}) (bool, error) {
	return llm.HasPending(), nil
}

func (s *llmSchema) fork(_ context.Context, llm *core.LLM, _ struct {
	Label string
}) (*core.LLM, error) {
	// The label participates in the returned object's ID, which is what makes
	// the fork evaluate independently; the state itself is just a clone.
	return llm.Clone(), nil
}

// attempt is the pre-v1 spelling of fork.
func (s *llmSchema) attempt(_ context.Context, llm *core.LLM, _ struct {
	Number int
}) (*core.LLM, error) {
	return llm.Clone(), nil
}

func (s *llmSchema) llm(ctx context.Context, parent *core.Query, args struct {
	Model    dagql.Optional[dagql.String]
	Provider dagql.Optional[dagql.String]
	// Legacy cap on API calls, only exposed to pre-v1 module views; v1+
	// callers pass maxSteps to loop() instead.
	MaxAPICalls dagql.Optional[dagql.Int] `name:"maxAPICalls"`
}) (*core.LLM, error) {
	llm, err := parent.NewLLM(ctx, args.Model.Value.String(), args.Provider.Value.String())
	if err != nil {
		return nil, err
	}
	if args.MaxAPICalls.Valid && args.MaxAPICalls.Value.Int() > 0 {
		llm = llm.WithMaxAPICalls(args.MaxAPICalls.Value.Int())
	}
	return llm, nil
}

func (s *llmSchema) messages(_ context.Context, llm *core.LLM, _ struct{}) ([]*core.LLMMessage, error) {
	return llm.Messages, nil
}

func (s *llmSchema) history(ctx context.Context, llm *core.LLM, _ struct{}) ([]string, error) {
	return llm.History(ctx)
}

func (s *llmSchema) transcript(ctx context.Context, llm *core.LLM, _ struct{}) (string, error) {
	return llm.Transcript(), nil
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
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.Null[*core.Binding](), err
	}
	return llm.BindResult(ctx, srv, args.Name)
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
