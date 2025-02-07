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
		dagql.Func("llm", s.llm).
			Doc(`Initialize a Large Language Model (LLM)`),
	}.Install(s.srv)
	llmType := dagql.Fields[*core.Llm]{
		dagql.Func("model", s.model).
			Doc("return the model used by the llm"),
		dagql.Func("history", s.history).
			Doc("return the llm message history"),
		dagql.Func("ask", s.ask).
			Doc("send a single prompt to the llm, and return its reply as a string").
			ArgDoc("prompt", "The prompt to send"),
		dagql.Func("lastReply", s.lastReply).
			Doc("return the last llm reply from the history"),
		dagql.Func("withPrompt", s.withPrompt).
			Doc("append a prompt to the llm context").
			ArgDoc("prompt", "The prompt to send").
			ArgDoc("lazy", "Buffer the prompt locally without sending"),
		dagql.Func("withPromptFile", s.withPromptFile).
			Doc("append the contents of a file to the llm context").
			ArgDoc("file", "The file to read the prompt from").
			ArgDoc("lazy", "Buffer the prompt locally without sending"),
		dagql.Func("sync", s.sync).
			Doc("synchronize the llm state: send outstanding prompts, process replies and tool calls").
			ArgDoc("maxLoops", "The maximum number of loops to allow."),
		dagql.Func("tools", s.tools).
			Doc("print documentation for available tools"),
	}
	llmType.Install(s.srv)
	s.srv.SetMiddleware(core.LlmMiddleware{Server: s.srv})
}

func (s *llmSchema) model(ctx context.Context, llm *core.Llm, args struct{}) (dagql.String, error) {
	return dagql.NewString(llm.Config.Model), nil
}

func (s *llmSchema) ask(ctx context.Context, llm *core.Llm, args struct {
	Prompt string
}) (dagql.String, error) {
	reply, err := llm.Ask(ctx, args.Prompt, s.srv)
	if err != nil {
		return "", err
	}
	return dagql.NewString(reply), nil
}

func (s *llmSchema) lastReply(ctx context.Context, llm *core.Llm, args struct{}) (dagql.String, error) {
	reply, err := llm.LastReply()
	if err != nil {
		return "", err
	}
	return dagql.NewString(reply), nil
}
func (s *llmSchema) withPrompt(ctx context.Context, llm *core.Llm, args struct {
	Prompt string
	Lazy   dagql.Optional[dagql.Boolean]
	Vars   dagql.Optional[dagql.ArrayInput[dagql.String]]
}) (*core.Llm, error) {
	var vars []string
	if args.Vars.Valid {
		for _, v := range args.Vars.Value {
			vars = append(vars, v.String())
		}
	}
	lazy := args.Lazy.GetOr(dagql.NewBoolean(false)).Bool()
	return llm.WithPrompt(ctx, args.Prompt, lazy, vars, s.srv)
}

func (s *llmSchema) withPromptFile(ctx context.Context, llm *core.Llm, args struct {
	File core.FileID
	Lazy dagql.Optional[dagql.Boolean]
	Vars dagql.Optional[dagql.ArrayInput[dagql.String]]
}) (*core.Llm, error) {
	file, err := args.File.Load(ctx, s.srv)
	if err != nil {
		return nil, err
	}
	var vars []string
	if args.Vars.Valid {
		for _, v := range args.Vars.Value {
			vars = append(vars, v.String())
		}
	}
	lazy := args.Lazy.GetOr(dagql.NewBoolean(false)).Bool()
	return llm.WithPromptFile(ctx, file.Self, lazy, vars, s.srv)
}

func (s *llmSchema) sync(ctx context.Context, llm *core.Llm, args struct {
	MaxLoops dagql.Optional[dagql.Int]
}) (*core.Llm, error) {
	maxLoops := args.MaxLoops.GetOr(0).Int()
	return llm.Sync(ctx, maxLoops, s.srv)
}

func (s *llmSchema) llm(ctx context.Context, parent *core.Query, _ struct{}) (*core.Llm, error) {
	return core.NewLlm(ctx, parent, s.srv)
}

func (s *llmSchema) history(ctx context.Context, llm *core.Llm, _ struct{}) (dagql.Array[dagql.String], error) {
	history, err := llm.History()
	if err != nil {
		return nil, err
	}
	return dagql.NewStringArray(history...), nil
}

func (s *llmSchema) tools(ctx context.Context, llm *core.Llm, _ struct{}) (dagql.String, error) {
	doc, err := llm.ToolsDoc(ctx, s.srv)
	return dagql.NewString(doc), err
}
