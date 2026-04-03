package dagql

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/dagql/call"
	"github.com/vektah/gqlparser/v2/ast"
)

type resultCallOptionalInput interface {
	resultCallOptionalValue() (Input, bool)
}

type resultCallArrayInput interface {
	resultCallArrayValues() []Input
}

type resultCallInputObject interface {
	resultCallInputObjectFields() []inputObjectField
}

func idInputDebugString(id *call.ID) string {
	if id == nil {
		return "<nil>"
	}
	if id.IsHandle() {
		return id.Display()
	}
	return string(id.Digest())
}

func resultCallRefFromResult(ctx context.Context, res AnyResult) (*ResultCallRef, error) {
	if res == nil {
		return nil, fmt.Errorf("nil result")
	}
	if typ := res.Type(); typ != nil && typ.Name() == "Query" {
		return nil, nil
	}
	shared := res.cacheSharedResult()
	var frame *ResultCall
	if shared != nil {
		frame = shared.loadResultCall()
	}
	if shared == nil || frame == nil {
		if cache, err := EngineCache(ctx); err == nil {
			reason := "missing_shared_result"
			if shared != nil {
				reason = "missing_result_call_frame"
			}
			cache.traceResultCallRefFromResultFailed(ctx, res, reason)
		}
		return nil, fmt.Errorf("result %T has no call frame", res)
	}
	if shared.id == 0 {
		return &ResultCallRef{Call: frame.clone()}, nil
	}
	return &ResultCallRef{ResultID: uint64(shared.id), shared: shared}, nil
}

func resultCallRefFromIDInput(ctx context.Context, id *call.ID) (*ResultCallRef, error) {
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
	return resultCallRefFromResult(ctx, val)
}

func resultCallArgFromInput(ctx context.Context, name string, input Input, sensitive bool) (*ResultCallArg, error) {
	if input == nil {
		return nil, fmt.Errorf("nil input for arg %q", name)
	}
	lit, err := resultCallLiteralFromInput(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("arg %q: %w", name, err)
	}
	return &ResultCallArg{
		Name:        name,
		IsSensitive: sensitive,
		Value:       lit,
	}, nil
}

func resultCallLiteralFromInput(ctx context.Context, input Input) (*ResultCallLiteral, error) {
	if input == nil {
		return &ResultCallLiteral{Kind: ResultCallLiteralKindNull}, nil
	}
	if idable, ok := input.(IDable); ok {
		id, err := idable.ID()
		if err != nil {
			return nil, fmt.Errorf("ID input %T is invalid: %w", input, err)
		}
		ref, err := resultCallRefFromIDInput(ctx, id)
		if err != nil {
			return nil, err
		}
		return &ResultCallLiteral{
			Kind:      ResultCallLiteralKindResultRef,
			ResultRef: ref,
		}, nil
	}
	if opt, ok := input.(resultCallOptionalInput); ok {
		val, valid := opt.resultCallOptionalValue()
		if !valid {
			return &ResultCallLiteral{Kind: ResultCallLiteralKindNull}, nil
		}
		return resultCallLiteralFromInput(ctx, val)
	}
	if arr, ok := input.(resultCallArrayInput); ok {
		values := arr.resultCallArrayValues()
		items := make([]*ResultCallLiteral, 0, len(values))
		for _, value := range values {
			item, err := resultCallLiteralFromInput(ctx, value)
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		}
		return &ResultCallLiteral{
			Kind:      ResultCallLiteralKindList,
			ListItems: items,
		}, nil
	}
	if obj, ok := input.(resultCallInputObject); ok {
		decodedFields := obj.resultCallInputObjectFields()
		if decodedFields == nil {
			return nil, fmt.Errorf("input object %T is missing decoded fields", input)
		}
		fields := make([]*ResultCallArg, 0, len(decodedFields))
		for _, field := range decodedFields {
			arg, err := resultCallArgFromInput(ctx, field.name, field.value, false)
			if err != nil {
				return nil, fmt.Errorf("field %q: %w", field.name, err)
			}
			fields = append(fields, arg)
		}
		return &ResultCallLiteral{
			Kind:         ResultCallLiteralKindObject,
			ObjectFields: fields,
		}, nil
	}
	return resultCallLiteralFromCallLiteral(ctx, input.ToLiteral())
}

func resultCallLiteralFromCallLiteral(ctx context.Context, lit call.Literal) (*ResultCallLiteral, error) {
	switch v := lit.(type) {
	case nil:
		return &ResultCallLiteral{Kind: ResultCallLiteralKindNull}, nil
	case *call.LiteralNull:
		return &ResultCallLiteral{Kind: ResultCallLiteralKindNull}, nil
	case *call.LiteralBool:
		return &ResultCallLiteral{Kind: ResultCallLiteralKindBool, BoolValue: v.Value()}, nil
	case *call.LiteralInt:
		return &ResultCallLiteral{Kind: ResultCallLiteralKindInt, IntValue: v.Value()}, nil
	case *call.LiteralFloat:
		return &ResultCallLiteral{Kind: ResultCallLiteralKindFloat, FloatValue: v.Value()}, nil
	case *call.LiteralString:
		return &ResultCallLiteral{Kind: ResultCallLiteralKindString, StringValue: v.Value()}, nil
	case *call.LiteralEnum:
		return &ResultCallLiteral{Kind: ResultCallLiteralKindEnum, EnumValue: v.Value()}, nil
	case *call.LiteralDigestedString:
		return &ResultCallLiteral{
			Kind:                 ResultCallLiteralKindDigestedString,
			DigestedStringValue:  v.Value(),
			DigestedStringDigest: v.Digest(),
		}, nil
	case *call.LiteralID:
		ref, err := resultCallRefFromIDInput(ctx, v.Value())
		if err != nil {
			return nil, err
		}
		return &ResultCallLiteral{
			Kind:      ResultCallLiteralKindResultRef,
			ResultRef: ref,
		}, nil
	case *call.LiteralList:
		items := make([]*ResultCallLiteral, 0, v.Len())
		for _, item := range v.Values() {
			converted, err := resultCallLiteralFromCallLiteral(ctx, item)
			if err != nil {
				return nil, err
			}
			items = append(items, converted)
		}
		return &ResultCallLiteral{
			Kind:      ResultCallLiteralKindList,
			ListItems: items,
		}, nil
	case *call.LiteralObject:
		fields := make([]*ResultCallArg, 0, v.Len())
		for _, field := range v.Args() {
			converted, err := resultCallLiteralFromCallLiteral(ctx, field.Value())
			if err != nil {
				return nil, fmt.Errorf("field %q: %w", field.Name(), err)
			}
			fields = append(fields, &ResultCallArg{
				Name:        field.Name(),
				IsSensitive: field.IsSensitive(),
				Value:       converted,
			})
		}
		return &ResultCallLiteral{
			Kind:         ResultCallLiteralKindObject,
			ObjectFields: fields,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported input literal %T", lit)
	}
}

func handleIDFromResultCallRef(ctx context.Context, ref *ResultCallRef) (*call.ID, error) {
	if err := ref.Validate(); err != nil {
		return nil, err
	}
	cache, err := EngineCache(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve result ref: %w", err)
	}
	if ref.Call != nil {
		resultID, err := cache.resultIDForCall(ctx, ref.Call)
		if err != nil {
			return nil, err
		}
		ref = &ResultCallRef{ResultID: uint64(resultID)}
	}
	res, _, _, err := cache.sharedResultByResultID(ctx, "", sharedResultID(ref.ResultID), sharedResultLookupExact)
	if err != nil {
		return nil, err
	}
	var gqlType *ast.Type
	if frame := res.loadResultCall(); frame != nil && frame.Type != nil {
		gqlType = frame.Type.toAST()
	}
	if gqlType == nil && res.self != nil {
		gqlType = res.self.Type()
	}
	if gqlType == nil {
		return nil, fmt.Errorf("result ref %d is missing a GraphQL type", ref.ResultID)
	}
	return call.NewEngineResultID(ref.ResultID, call.NewType(gqlType)), nil
}

func inputValueFromResultCallLiteral(ctx context.Context, lit *ResultCallLiteral) (any, error) {
	if lit == nil {
		return nil, nil
	}
	switch lit.Kind {
	case ResultCallLiteralKindNull:
		return nil, nil
	case ResultCallLiteralKindBool:
		return lit.BoolValue, nil
	case ResultCallLiteralKindInt:
		return lit.IntValue, nil
	case ResultCallLiteralKindFloat:
		return lit.FloatValue, nil
	case ResultCallLiteralKindString:
		return lit.StringValue, nil
	case ResultCallLiteralKindEnum:
		return lit.EnumValue, nil
	case ResultCallLiteralKindDigestedString:
		return lit.DigestedStringValue, nil
	case ResultCallLiteralKindResultRef:
		return handleIDFromResultCallRef(ctx, lit.ResultRef)
	case ResultCallLiteralKindList:
		values := make([]any, 0, len(lit.ListItems))
		for _, item := range lit.ListItems {
			val, err := inputValueFromResultCallLiteral(ctx, item)
			if err != nil {
				return nil, err
			}
			values = append(values, val)
		}
		return values, nil
	case ResultCallLiteralKindObject:
		values := make(map[string]any, len(lit.ObjectFields))
		for _, field := range lit.ObjectFields {
			if field == nil {
				continue
			}
			val, err := inputValueFromResultCallLiteral(ctx, field.Value)
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
