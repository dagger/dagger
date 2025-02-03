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
			Doc("send a single message to the llm, and return its reply as a string"),
		dagql.Func("lastReply", s.lastReply).
			Doc("return the last llm reply from the history"),
		dagql.Func("withPrompt", s.withPrompt).
			Doc("append a prompt to the llm context"),
		dagql.Func("sync", s.sync).
			Doc("synchronize the llm state: send outstanding prompts, process replies and tool calls"),
	}
	llmType.Install(s.srv)
	// s.srv.SetMiddleware(core.LlmMiddleware{Server: s.srv})
}

func (s *llmSchema) model(ctx context.Context, llm *core.Llm, args struct{}) (dagql.String, error) {
	return dagql.NewString(llm.Config.Model), nil
}

func (s *llmSchema) ask(ctx context.Context, llm *core.Llm, args struct {
	Prompt string
}) (dagql.String, error) {
	reply, err := llm.Ask(ctx, args.Prompt)
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
}) (*core.Llm, error) {
	return llm.WithPrompt(ctx, args.Prompt, args.Lazy.GetOr(dagql.NewBoolean(false)).Bool())
}

func (s *llmSchema) sync(ctx context.Context, llm *core.Llm, args struct{}) (*core.Llm, error) {
	return llm.Run(ctx, 0)
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
