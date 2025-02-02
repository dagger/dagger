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
	}
	llmType.Install(s.srv)
	//slog.Debug("introspecting types for llm type extension", "nTypes", len(s.srv.Schema().Types))
	//for typename, _ := range s.srv.Schema().Types {
	//	slog.Debug("introspecting type for llm type extension", "typename", typename)
	//	objType, isObject := s.srv.ObjectType(typename)
	//	if !isObject {
	//		continue
	//	}
	//	idType, ok := objType.IDType()
	//	if !ok {
	//		continue
	//	}
	//	llmType = append(llmType, dagql.Field[*core.Llm]{
	//		Spec: dagql.FieldSpec{
	//			Name:        "set" + typename,
	//			Description: fmt.Sprintf("Save a %s to the given key in the llm state", typename),
	//			Type:        core.Llm{},
	//			Args: dagql.InputSpecs{
	//				{
	//					Name:        "key",
	//					Description: fmt.Sprintf("The key to save the %s at", typename),
	//					Type:        dagql.String(""),
	//				},
	//				{
	//					Name:        "value",
	//					Description: fmt.Sprintf("The value of the %s to save", typename),
	//					Type:        idType,
	//				},
	//			},
	//		},
	//		Func: func(ctx context.Context, llm dagql.Instance[*core.Llm], args map[string]dagql.Input) (dagql.Typed, error) {
	//			key := args["key"].(dagql.String).String()
	//			value := args["value"].(dagql.Object)
	//			return llm.Self.Set(ctx, key, value)
	//		},
	//		CacheKeyFunc: nil,
	//	})
	//}
	//llmType.Install(s.srv)
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
