package dagql

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/dagql/call"
	"github.com/vektah/gqlparser/v2/ast"
)

type frameOptionalInput interface {
	frameOptionalValue() (Input, bool)
}

type frameArrayInput interface {
	frameArrayValues() []Input
}

type frameInputObject interface {
	frameInputObjectFields() []inputObjectField
}

func idInputDebugString(id *call.ID) string {
	if id == nil {
		return "<nil>"
	}
	enc, err := id.Encode()
	if err == nil {
		return enc
	}
	return "<encode-error>"
}

func frameRefFromResult(res AnyResult) (*ResultCallFrameRef, error) {
	if res == nil {
		return nil, fmt.Errorf("nil result")
	}
	if typ := res.Type(); typ != nil && typ.Name() == "Query" {
		return nil, nil
	}
	shared := res.cacheSharedResult()
	if shared == nil || shared.id == 0 {
		return nil, fmt.Errorf("result %T is not cache-backed", res)
	}
	return &ResultCallFrameRef{ResultID: uint64(shared.id)}, nil
}

func frameRefFromIDInput(ctx context.Context, id *call.ID) (*ResultCallFrameRef, error) {
	if id == nil {
		return nil, fmt.Errorf("nil ID input")
	}
	if !id.IsHandle() {
		return nil, fmt.Errorf("recipe-form IDs are not valid inputs: %s", idInputDebugString(id))
	}
	srv := CurrentDagqlServer(ctx)
	if srv == nil {
		return nil, fmt.Errorf("cannot resolve ID input %q without dagql server", idInputDebugString(id))
	}
	val, err := srv.Load(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("load ID input %q: %w", idInputDebugString(id), err)
	}
	return frameRefFromResult(val)
}

func frameArgFromInput(ctx context.Context, name string, input Input, sensitive bool) (*ResultCallFrameArg, error) {
	if input == nil {
		return nil, fmt.Errorf("nil input for arg %q", name)
	}
	lit, err := frameLiteralFromInput(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("arg %q: %w", name, err)
	}
	return &ResultCallFrameArg{
		Name:        name,
		IsSensitive: sensitive,
		Value:       lit,
	}, nil
}

func frameLiteralFromInput(ctx context.Context, input Input) (*ResultCallFrameLiteral, error) {
	if input == nil {
		return &ResultCallFrameLiteral{Kind: ResultCallFrameLiteralKindNull}, nil
	}
	if idable, ok := input.(IDable); ok {
		id := idable.ID()
		if id == nil {
			return nil, fmt.Errorf("ID input %T is missing an ID", input)
		}
		ref, err := frameRefFromIDInput(ctx, id)
		if err != nil {
			return nil, err
		}
		return &ResultCallFrameLiteral{
			Kind:      ResultCallFrameLiteralKindResultRef,
			ResultRef: ref,
		}, nil
	}
	if opt, ok := input.(frameOptionalInput); ok {
		val, valid := opt.frameOptionalValue()
		if !valid {
			return &ResultCallFrameLiteral{Kind: ResultCallFrameLiteralKindNull}, nil
		}
		return frameLiteralFromInput(ctx, val)
	}
	if arr, ok := input.(frameArrayInput); ok {
		values := arr.frameArrayValues()
		items := make([]*ResultCallFrameLiteral, 0, len(values))
		for _, value := range values {
			item, err := frameLiteralFromInput(ctx, value)
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		}
		return &ResultCallFrameLiteral{
			Kind:      ResultCallFrameLiteralKindList,
			ListItems: items,
		}, nil
	}
	if obj, ok := input.(frameInputObject); ok {
		decodedFields := obj.frameInputObjectFields()
		if decodedFields == nil {
			return nil, fmt.Errorf("input object %T is missing decoded fields", input)
		}
		fields := make([]*ResultCallFrameArg, 0, len(decodedFields))
		for _, field := range decodedFields {
			arg, err := frameArgFromInput(ctx, field.name, field.value, false)
			if err != nil {
				return nil, fmt.Errorf("field %q: %w", field.name, err)
			}
			fields = append(fields, arg)
		}
		return &ResultCallFrameLiteral{
			Kind:         ResultCallFrameLiteralKindObject,
			ObjectFields: fields,
		}, nil
	}
	return frameLiteralFromCallLiteral(ctx, input.ToLiteral())
}

func frameLiteralFromCallLiteral(ctx context.Context, lit call.Literal) (*ResultCallFrameLiteral, error) {
	switch v := lit.(type) {
	case nil:
		return &ResultCallFrameLiteral{Kind: ResultCallFrameLiteralKindNull}, nil
	case *call.LiteralNull:
		return &ResultCallFrameLiteral{Kind: ResultCallFrameLiteralKindNull}, nil
	case *call.LiteralBool:
		return &ResultCallFrameLiteral{Kind: ResultCallFrameLiteralKindBool, BoolValue: v.Value()}, nil
	case *call.LiteralInt:
		return &ResultCallFrameLiteral{Kind: ResultCallFrameLiteralKindInt, IntValue: v.Value()}, nil
	case *call.LiteralFloat:
		return &ResultCallFrameLiteral{Kind: ResultCallFrameLiteralKindFloat, FloatValue: v.Value()}, nil
	case *call.LiteralString:
		return &ResultCallFrameLiteral{Kind: ResultCallFrameLiteralKindString, StringValue: v.Value()}, nil
	case *call.LiteralEnum:
		return &ResultCallFrameLiteral{Kind: ResultCallFrameLiteralKindEnum, EnumValue: v.Value()}, nil
	case *call.LiteralDigestedString:
		return &ResultCallFrameLiteral{
			Kind:                 ResultCallFrameLiteralKindDigestedString,
			DigestedStringValue:  v.Value(),
			DigestedStringDigest: v.Digest(),
		}, nil
	case *call.LiteralID:
		ref, err := frameRefFromIDInput(ctx, v.Value())
		if err != nil {
			return nil, err
		}
		return &ResultCallFrameLiteral{
			Kind:      ResultCallFrameLiteralKindResultRef,
			ResultRef: ref,
		}, nil
	case *call.LiteralList:
		items := make([]*ResultCallFrameLiteral, 0, v.Len())
		for _, item := range v.Values() {
			converted, err := frameLiteralFromCallLiteral(ctx, item)
			if err != nil {
				return nil, err
			}
			items = append(items, converted)
		}
		return &ResultCallFrameLiteral{
			Kind:      ResultCallFrameLiteralKindList,
			ListItems: items,
		}, nil
	case *call.LiteralObject:
		fields := make([]*ResultCallFrameArg, 0, v.Len())
		for _, field := range v.Args() {
			converted, err := frameLiteralFromCallLiteral(ctx, field.Value())
			if err != nil {
				return nil, fmt.Errorf("field %q: %w", field.Name(), err)
			}
			fields = append(fields, &ResultCallFrameArg{
				Name:        field.Name(),
				IsSensitive: field.IsSensitive(),
				Value:       converted,
			})
		}
		return &ResultCallFrameLiteral{
			Kind:         ResultCallFrameLiteralKindObject,
			ObjectFields: fields,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported input literal %T", lit)
	}
}

func handleIDFromFrameRef(ctx context.Context, ref *ResultCallFrameRef) (*call.ID, error) {
	if ref == nil || ref.ResultID == 0 {
		return nil, fmt.Errorf("missing result ref")
	}
	srv := CurrentDagqlServer(ctx)
	if srv == nil || srv.Cache == nil {
		return nil, fmt.Errorf("cannot resolve result ref %d without dagql server cache", ref.ResultID)
	}
	base, ok := srv.Cache.cache.(*cache)
	if !ok {
		return nil, fmt.Errorf("unexpected cache implementation %T", srv.Cache.cache)
	}
	res, err := base.sharedResultByResultID(sharedResultID(ref.ResultID))
	if err != nil {
		return nil, err
	}
	var gqlType *ast.Type
	if res.resultCallFrame != nil && res.resultCallFrame.Type != nil {
		gqlType = res.resultCallFrame.Type.toAST()
	}
	if gqlType == nil && res.self != nil {
		gqlType = res.self.Type()
	}
	if gqlType == nil {
		return nil, fmt.Errorf("result ref %d is missing a GraphQL type", ref.ResultID)
	}
	return call.NewEngineResultID(ref.ResultID, call.NewType(gqlType)), nil
}

func inputValueFromFrameLiteral(ctx context.Context, lit *ResultCallFrameLiteral) (any, error) {
	if lit == nil {
		return nil, nil
	}
	switch lit.Kind {
	case ResultCallFrameLiteralKindNull:
		return nil, nil
	case ResultCallFrameLiteralKindBool:
		return lit.BoolValue, nil
	case ResultCallFrameLiteralKindInt:
		return lit.IntValue, nil
	case ResultCallFrameLiteralKindFloat:
		return lit.FloatValue, nil
	case ResultCallFrameLiteralKindString:
		return lit.StringValue, nil
	case ResultCallFrameLiteralKindEnum:
		return lit.EnumValue, nil
	case ResultCallFrameLiteralKindDigestedString:
		return lit.DigestedStringValue, nil
	case ResultCallFrameLiteralKindResultRef:
		return handleIDFromFrameRef(ctx, lit.ResultRef)
	case ResultCallFrameLiteralKindList:
		values := make([]any, 0, len(lit.ListItems))
		for _, item := range lit.ListItems {
			val, err := inputValueFromFrameLiteral(ctx, item)
			if err != nil {
				return nil, err
			}
			values = append(values, val)
		}
		return values, nil
	case ResultCallFrameLiteralKindObject:
		values := make(map[string]any, len(lit.ObjectFields))
		for _, field := range lit.ObjectFields {
			if field == nil {
				continue
			}
			val, err := inputValueFromFrameLiteral(ctx, field.Value)
			if err != nil {
				return nil, fmt.Errorf("field %q: %w", field.Name, err)
			}
			values[field.Name] = val
		}
		return values, nil
	default:
		return nil, fmt.Errorf("unsupported frame literal kind %q", lit.Kind)
	}
}
