package schema

import (
	"context"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
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
				dagql.Arg("maxAPICalls").Doc("Cap the number of API calls for this LLM"),
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
		dagql.Func("lastReply", s.lastReply).
			Doc("return the last llm reply from the history"),
		dagql.Func("withEnv", s.withEnv).
			Doc("allow the LLM to interact with an environment via MCP"),
		dagql.Func("env", s.env).
			Doc("return the LLM's current environment"),
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
		dagql.Func("withCaller", s.withCaller).
			Doc("Provide the calling object as an input to the LLM environment").
			Args(
				dagql.Arg("name").Doc("The name of the binding"),
				dagql.Arg("description").Doc("The description of the input"),
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
		dagql.Func("withoutDefaultSystemPrompt", s.withoutDefaultSystemPrompt).
			Doc("Disable the default system prompt"),
		dagql.Func("withBlockedFunction", s.withBlockedFunction).
			Doc("Return a new LLM with the specified tool disabled").
			Args(
				dagql.Arg("typeName").Doc("The type name whose field will be disabled"),
				dagql.Arg("fieldName").Doc("The field name to disable"),
			),
		dagql.NodeFunc("sync", func(ctx context.Context, self dagql.ObjectResult[*core.LLM], _ struct{}) (res dagql.Result[dagql.ID[*core.LLM]], _ error) {
			var inst dagql.Result[*core.LLM]
			if err := srv.Select(ctx, self, &inst, dagql.Selector{
				Field: "loop",
			}); err != nil {
				return res, err
			}
			id := dagql.NewID[*core.LLM](inst.ID())
			return dagql.NewResultForCurrentID(ctx, id)
		}).
			Doc("synchronize LLM state"),
		dagql.Func("loop", s.loop).
			Doc("Loop completing tool calls until the LLM ends its turn"),
		dagql.Func("step", s.step).
			// Deprecated("use sync").
			Doc("Returns an LLM that will only sync one step instead of looping"),
		dagql.Func("hasPrompt", s.hasPrompt).
			// Deprecated("use sync").
			Doc("Indicates that the LLM can be synced or stepped"),
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

func (s *llmSchema) withCaller(ctx context.Context, llm *core.LLM, args struct {
	Name        string
	Description string
}) (*core.LLM, error) {
	return llm.WithCaller(ctx, args.Name, args.Description)
}

func (s *llmSchema) env(ctx context.Context, llm *core.LLM, args struct{}) (res dagql.ObjectResult[*core.Env], _ error) {
	if err := llm.Sync(ctx); err != nil {
		return res, err
	}
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

func (s *llmSchema) lastReply(ctx context.Context, llm *core.LLM, args struct{}) (dagql.String, error) {
	reply, err := llm.LastReply(ctx)
	if err != nil {
		return "", err
	}
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

func (s *llmSchema) withoutDefaultSystemPrompt(ctx context.Context, llm *core.LLM, args struct{}) (*core.LLM, error) {
	return llm.WithoutDefaultSystemPrompt(), nil
}

func (s *llmSchema) withBlockedFunction(ctx context.Context, llm *core.LLM, args struct {
	TypeName  string
	FieldName string
}) (*core.LLM, error) {
	return llm.WithBlockedFunction(ctx, args.TypeName, args.FieldName)
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

func (s *llmSchema) loop(ctx context.Context, llm *core.LLM, args struct{}) (*core.LLM, error) {
	return llm, llm.Sync(ctx)
}

func (s *llmSchema) step(ctx context.Context, llm *core.LLM, args struct{}) (*core.LLM, error) {
	return llm.Step(), nil
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
	Model       dagql.Optional[dagql.String]
	MaxAPICalls dagql.Optional[dagql.Int] `name:"maxAPICalls"`
}) (*core.LLM, error) {
	var model string
	if args.Model.Valid {
		model = args.Model.Value.String()
	}
	var maxAPICalls int
	if args.MaxAPICalls.Valid {
		maxAPICalls = args.MaxAPICalls.Value.Int()
	}
	return parent.NewLLM(ctx, model, maxAPICalls)
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
