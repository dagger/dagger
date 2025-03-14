package schema

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

type llmSchema struct {
	srv *dagql.Server
}

var _ SchemaResolvers = &llmSchema{}

func (s llmSchema) Install() {
	dagql.Fields[*core.Query]{
		dagql.Func("llm", s.llm).
			Doc(`Initialize a Large Language Model (LLM)`).
			ArgDoc("model", "Model to use").
			ArgDoc("maxAPICalls", "Cap the number of API calls for this LLM"),
	}.Install(s.srv)
	dagql.Fields[*core.LLM]{
		dagql.Func("model", s.model).
			Doc("return the model used by the llm"),
		dagql.Func("provider", s.provider).
			Doc("return the provider used by the llm"),
		dagql.Func("history", s.history).
			Doc("return the llm message history"),
		dagql.Func("historyJSON", s.historyJSON).
			Doc("return the raw llm message history as json"),
		dagql.Func("lastReply", s.lastReply).
			Doc("return the last llm reply from the history"),
		dagql.Func("withModel", s.withModel).
			Doc("swap out the llm model").
			ArgDoc("model", "The model to use"),
		dagql.Func("withPrompt", s.withPrompt).
			Doc("append a prompt to the llm context").
			ArgDoc("prompt", "The prompt to send"),
		dagql.NodeFunc("__mcp", func(ctx context.Context, self dagql.Instance[*core.LLM], _ struct{}) (dagql.ID[*core.LLM], error) {
			var zero dagql.ID[*core.LLM]
			return zero, self.Self.MCP(ctx, s.srv)
		}).
			Doc("instantiates an mcp server"),
		dagql.Func("withPromptFile", s.withPromptFile).
			Doc("append the contents of a file to the llm context").
			ArgDoc("file", "The file to read the prompt from"),
		dagql.Func("withQuery", s.withQuery).
			Doc("Provide the entire Query object to the LLM"),
		dagql.Func("withPromptVar", s.withPromptVar).
			Doc("Add a string variable to the LLM's environment").
			ArgDoc("name", "The variable name").
			ArgDoc("value", "The variable value"),
		dagql.Func("withSystemPrompt", s.withSystemPrompt).
			Doc("Add a system prompt to the LLM's environment").
			ArgDoc("prompt", "The system prompt to send"),
		dagql.Func("setString", s.withPromptVar).
			Doc("Add a string variable to the LLM's environment").
			ArgDoc("name", "The variable name").
			ArgDoc("value", "The variable value"),
		dagql.Func("getString", s.getString).
			Doc("Get a string variable from the LLM's environment").
			ArgDoc("name", "The variable name"),
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
		dagql.Func("variables", s.variables).
			Doc("list variables in the LLM environment"),
		dagql.Func("currentType", s.currentType).
			Doc("returns the type of the current state"),
		dagql.Func("tokenUsage", s.tokenUsage).
			Doc("returns the token usage of the current state"),
	}.Install(s.srv)
	dagql.Fields[*core.LLMVariable]{}.Install(s.srv)
	dagql.Fields[*core.LLMTokenUsage]{}.Install(s.srv)
	hook := core.LLMHook{Server: s.srv}
	llmObjType, ok := s.srv.ObjectType(new(core.LLM).Type().Name())
	if !ok {
		panic("llm type not found after dagql install")
	}
	hook.ExtendLLMType(llmObjType)
	s.srv.AddInstallHook(hook)
}

func (s *llmSchema) model(ctx context.Context, llm *core.LLM, args struct{}) (string, error) {
	return llm.Endpoint.Model, nil
}

func (s *llmSchema) provider(ctx context.Context, llm *core.LLM, args struct{}) (string, error) {
	return string(llm.Endpoint.Provider), nil
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
	return llm.WithModel(ctx, args.Model, s.srv)
}

func (s *llmSchema) withPrompt(ctx context.Context, llm *core.LLM, args struct {
	Prompt string
}) (*core.LLM, error) {
	return llm.WithPrompt(ctx, args.Prompt, s.srv)
}

func (s *llmSchema) withQuery(ctx context.Context, llm *core.LLM, args struct{}) (*core.LLM, error) {
	return llm.With(s.srv.Root()), nil
}

func (s *llmSchema) withSystemPrompt(ctx context.Context, llm *core.LLM, args struct {
	Prompt string
}) (*core.LLM, error) {
	return llm.WithSystemPrompt(args.Prompt), nil
}

func (s *llmSchema) withPromptVar(ctx context.Context, llm *core.LLM, args struct {
	Name  string
	Value string
}) (*core.LLM, error) {
	return llm.WithPromptVar(args.Name, args.Value), nil
}

func (s *llmSchema) getString(ctx context.Context, llm *core.LLM, args struct {
	Name string
}) (dagql.String, error) {
	val, err := llm.Get(ctx, s.srv, args.Name)
	if err != nil {
		return "", err
	}
	if str, ok := dagql.UnwrapAs[dagql.String](val); ok {
		return str, nil
	}
	return "", fmt.Errorf("expected string value for %q, got %T", args.Name, val)
}

func (s *llmSchema) withPromptFile(ctx context.Context, llm *core.LLM, args struct {
	File core.FileID
}) (*core.LLM, error) {
	file, err := args.File.Load(ctx, s.srv)
	if err != nil {
		return nil, err
	}
	return llm.WithPromptFile(ctx, file.Self, s.srv)
}

func (s *llmSchema) loop(ctx context.Context, llm *core.LLM, args struct{}) (*core.LLM, error) {
	return llm.Sync(ctx, s.srv)
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
	return core.NewLLM(ctx, parent, model, maxAPICalls)
}

func (s *llmSchema) history(ctx context.Context, llm *core.LLM, _ struct{}) (dagql.Array[dagql.String], error) {
	history, err := llm.History(ctx, s.srv)
	if err != nil {
		return nil, err
	}
	return dagql.NewStringArray(history...), nil
}

func (s *llmSchema) historyJSON(ctx context.Context, llm *core.LLM, _ struct{}) (dagql.String, error) {
	history, err := llm.HistoryJSON(ctx, s.srv)
	if err != nil {
		return "", err
	}
	return dagql.NewString(history), nil
}

func (s *llmSchema) tools(ctx context.Context, llm *core.LLM, _ struct{}) (dagql.String, error) {
	doc, err := llm.ToolsDoc(ctx, s.srv)
	return dagql.NewString(doc), err
}

func (s *llmSchema) variables(ctx context.Context, llm *core.LLM, _ struct{}) ([]*core.LLMVariable, error) {
	return llm.Variables(ctx, s.srv)
}

func (s *llmSchema) currentType(ctx context.Context, llm *core.LLM, _ struct{}) (dagql.Nullable[dagql.String], error) {
	return llm.CurrentType(ctx, s.srv)
}

func (s *llmSchema) tokenUsage(ctx context.Context, llm *core.LLM, _ struct{}) (*core.LLMTokenUsage, error) {
	return llm.TokenUsage(ctx, s.srv)
}
