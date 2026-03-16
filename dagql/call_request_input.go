package dagql

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/dagql/call"
)

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
	srv := CurrentDagqlServer(ctx)
	if srv == nil {
		return nil, fmt.Errorf("cannot resolve ID input %q without dagql server", id.DisplaySelf())
	}
	val, err := srv.Load(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("load ID input %q: %w", id.DisplaySelf(), err)
	}
	return frameRefFromResult(val)
}

func frameModuleFromCallModule(ctx context.Context, mod *call.Module) (*ResultCallFrameModule, error) {
	if mod == nil {
		return nil, nil
	}
	modID := mod.ID()
	if modID == nil {
		return nil, fmt.Errorf("module %q missing ID", mod.Name())
	}
	ref, err := frameRefFromIDInput(ctx, modID)
	if err != nil {
		return nil, fmt.Errorf("resolve module %q: %w", mod.Name(), err)
	}
	return &ResultCallFrameModule{
		ResultRef: ref,
		Name:      mod.Name(),
		Ref:       mod.Ref(),
		Pin:       mod.Pin(),
	}, nil
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

func callIDFromFrameRef(ctx context.Context, ref *ResultCallFrameRef) (*call.ID, error) {
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
	return base.persistedCallIDByResultID(ctx, sharedResultID(ref.ResultID))
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
		return callIDFromFrameRef(ctx, lit.ResultRef)
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

func ExtractRequestArgs(ctx context.Context, specs InputSpecs, req *CallRequest) (map[string]Input, error) {
	if req == nil {
		return nil, fmt.Errorf("request is nil")
	}
	view := req.View
	inputArgs := make(map[string]Input, len(req.Args))
	for _, argSpec := range specs.Inputs(view) {
		var requestArg *ResultCallFrameArg
		for _, arg := range req.Args {
			if arg != nil && arg.Name == argSpec.Name {
				requestArg = arg
				break
			}
		}
		switch {
		case requestArg != nil:
			inputVal, err := inputValueFromFrameLiteral(ctx, requestArg.Value)
			if err != nil {
				return nil, fmt.Errorf("request arg %q: %w", argSpec.Name, err)
			}
			input, err := argSpec.Type.Decoder().DecodeInput(inputVal)
			if err != nil {
				return nil, fmt.Errorf("request arg %q value as %T (%s) using %T: %w", argSpec.Name, argSpec.Type, argSpec.Type.Type(), argSpec.Type.Decoder(), err)
			}
			inputArgs[argSpec.Name] = input
		case argSpec.Default != nil:
			inputArgs[argSpec.Name] = argSpec.Default
		case argSpec.Type.Type().NonNull:
			return nil, fmt.Errorf("missing required argument: %q", argSpec.Name)
		}
	}
	return inputArgs, nil
}
