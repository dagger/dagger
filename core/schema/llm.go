package schema

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/slog"
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
	slog.Debug("introspecting types for llm type extension", "nTypes", len(s.srv.Schema().Types))
	for typename, _ := range s.srv.Schema().Types {
		slog.Debug("introspecting type for llm type extension", "typename", typename)
		objType, isObject := s.srv.ObjectType(typename)
		if !isObject {
			continue
		}
		idType, ok := objType.IDType()
		if !ok {
			continue
		}
		llmType = append(llmType,
			dagql.Field[*core.Llm]{
				Spec: dagql.FieldSpec{
					Name:        "set" + typename,
					Description: fmt.Sprintf("Save a %s to the given key in the llm state", typename),
					Type:        new(core.Llm),
					Args: dagql.InputSpecs{
						{
							Name:        "key",
							Description: fmt.Sprintf("The key to save the %s at", typename),
							Type:        dagql.String(""),
						},
						{
							Name:        "value",
							Description: fmt.Sprintf("The value of the %s to save", typename),
							Type:        idType,
						},
					},
				},
				Func: func(ctx context.Context, llm dagql.Instance[*core.Llm], args map[string]dagql.Input) (dagql.Typed, error) {
					key := args["key"].(dagql.String).String()
					id := args["value"].(dagql.IDType)
					return llm.Self.Set(ctx, key, id)
				},
				CacheKeyFunc: nil,
			},
			dagql.Field[*core.Llm]{
				Spec: dagql.FieldSpec{
					Name:        "get" + typename,
					Description: fmt.Sprintf("Retrieve a %s from the llm state", typename),
					Type:        objType.Typed(),
					Args: dagql.InputSpecs{
						{
							Name:        "key",
							Description: fmt.Sprintf("The key to retrieve the %s from", typename),
							Type:        dagql.String(""),
						},
					},
				},
				Func: func(ctx context.Context, llm dagql.Instance[*core.Llm], args map[string]dagql.Input) (dagql.Typed, error) {
					key := args["key"].(dagql.String).String()
					return llm.Self.Get(ctx, key)
				},
				CacheKeyFunc: nil,
			},
		)
	}
	llmType.Install(s.srv)
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
