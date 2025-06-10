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

func (s llmSchema) Install() {
	dagql.Fields[*core.Query]{
		dagql.FuncWithCacheKey("llm", s.llm, dagql.CachePerSession).
			Experimental("LLM support is not yet stabilized").
			Doc(`Initialize a Large Language Model (LLM)`).
			Args(
				dagql.Arg("model").Doc("Model to use"),
				dagql.Arg("maxAPICalls").Doc("Cap the number of API calls for this LLM"),
			),
	}.Install(s.srv)
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
		dagql.NodeFunc("__mcp", func(ctx context.Context, self dagql.Instance[*core.LLM], _ struct{}) (dagql.Nullable[core.Void], error) {
			return dagql.Null[core.Void](), self.Self.MCP(ctx, s.srv)
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
		dagql.NodeFunc("sync", func(ctx context.Context, self dagql.Instance[*core.LLM], _ struct{}) (dagql.ID[*core.LLM], error) {
			var zero dagql.ID[*core.LLM]
			var inst dagql.Instance[*core.LLM]
			if err := s.srv.Select(ctx, self, &inst, dagql.Selector{
				Field: "loop",
			}); err != nil {
				return zero, err
			}
			return dagql.NewID[*core.LLM](inst.ID()), nil
		}).
			Doc("synchronize LLM state"),
		dagql.Func("loop", s.loop).
			// Deprecated("use sync").
			Doc("synchronize LLM state"),
		dagql.Func("attempt", s.attempt).
			Doc("create a branch in the LLM's history"),
		dagql.Func("tools", s.tools).
			Doc("print documentation for available tools"),
		dagql.Func("bindResult", s.bindResult).
			Doc("returns the type of the current state"),
		dagql.Func("tokenUsage", s.tokenUsage).
			Doc("returns the token usage of the current state"),
	}.Install(s.srv)
	dagql.Fields[*core.LLMTokenUsage]{}.Install(s.srv)
}
func (s *llmSchema) withEnv(ctx context.Context, llm *core.LLM, args struct {
	Env core.EnvID
}) (*core.LLM, error) {
	env, err := args.Env.Load(ctx, s.srv)
	if err != nil {
		return nil, err
	}
	return llm.WithEnv(env.Self), nil
}

func (s *llmSchema) env(ctx context.Context, llm *core.LLM, args struct{}) (*core.Env, error) {
	if err := llm.Sync(ctx, s.srv); err != nil {
		return nil, err
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
	reply, err := llm.LastReply(ctx, s.srv)
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

func (s *llmSchema) withPromptFile(ctx context.Context, llm *core.LLM, args struct {
	File core.FileID
}) (*core.LLM, error) {
	file, err := args.File.Load(ctx, s.srv)
	if err != nil {
		return nil, err
	}
	return llm.WithPromptFile(ctx, file.Self)
}

func (s *llmSchema) loop(ctx context.Context, llm *core.LLM, args struct{}) (*core.LLM, error) {
	return llm, llm.Sync(ctx, s.srv)
}

func (s *llmSchema) attempt(_ context.Context, llm *core.LLM, _ struct {
	Number int
}) (*core.LLM, error) {
	// nothing to do; we've "forked" it by nature of changing the query
	return llm, nil
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
	return core.NewLLM(ctx, model, maxAPICalls)
}

func (s *llmSchema) history(ctx context.Context, llm *core.LLM, _ struct{}) ([]string, error) {
	return llm.History(ctx, s.srv)
}

func (s *llmSchema) historyJSON(ctx context.Context, llm *core.LLM, _ struct{}) (core.JSON, error) {
	return llm.HistoryJSON(ctx, s.srv)
}

func (s *llmSchema) historyJSONString(ctx context.Context, llm *core.LLM, _ struct{}) (string, error) {
	js, err := llm.HistoryJSON(ctx, s.srv)
	if err != nil {
		return "", err
	}
	return js.String(), nil
}

func (s *llmSchema) tools(ctx context.Context, llm *core.LLM, _ struct{}) (string, error) {
	return llm.ToolsDoc(s.srv)
}

func (s *llmSchema) bindResult(ctx context.Context, llm *core.LLM, args struct {
	Name string
}) (dagql.Nullable[*core.Binding], error) {
	return llm.BindResult(ctx, s.srv, args.Name)
}

func (s *llmSchema) tokenUsage(ctx context.Context, llm *core.LLM, _ struct{}) (*core.LLMTokenUsage, error) {
	return llm.TokenUsage(ctx, s.srv)
}
