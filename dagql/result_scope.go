package dagql

import "context"

// ResultScope replaces object references without changing the source results.
type ResultScope interface {
	Apply(context.Context, *Server, AnyObjectResult) (AnyObjectResult, error)
}

type HasResultScope interface {
	ResultScope() ResultScope
}

type resultScopeKey struct{}
type resultScopeContext struct{ scope ResultScope }

func WithResultScope(ctx context.Context, scope ResultScope) context.Context {
	return context.WithValue(ctx, resultScopeKey{}, resultScopeContext{scope})
}

func CurrentResultScope(ctx context.Context) ResultScope {
	value, _ := ctx.Value(resultScopeKey{}).(resultScopeContext)
	return value.scope
}

func resultScopeFromReceiver(ctx context.Context, self AnyResult) context.Context {
	if _, set := ctx.Value(resultScopeKey{}).(resultScopeContext); !set {
		if scoped, ok := UnwrapAs[HasResultScope](self); ok {
			if scope := scoped.ResultScope(); scope != nil {
				return WithResultScope(ctx, scope)
			}
		}
	}
	return ctx
}

func scopeObject(ctx context.Context, srv *Server, result AnyObjectResult) (AnyObjectResult, error) {
	if scope := CurrentResultScope(ctx); scope != nil {
		return scope.Apply(ctx, srv, result)
	}
	return result, nil
}

func scopeInput(ctx context.Context, srv *Server, scope ResultScope, input Input) (Input, bool, error) {
	value, changed, err := scopeInputValue(ctx, srv, scope, input)
	if err != nil || !changed {
		return input, false, err
	}
	updated, err := input.Decoder().DecodeInput(value)
	return updated, true, err
}

func scopeInputValue(ctx context.Context, srv *Server, scope ResultScope, input Input) (any, bool, error) {
	if optional, ok := input.(resultCallOptionalInput); ok {
		value, valid := optional.resultCallOptionalValue()
		if !valid {
			return nil, false, nil
		}
		return scopeInputValue(ctx, srv, scope, value)
	}
	if idable, ok := UnwrapAs[IDable](input); ok {
		id, err := idable.ID()
		if err != nil {
			return nil, false, err
		}
		object, err := srv.Load(WithResultScope(ctx, nil), id)
		if err != nil {
			return nil, false, err
		}
		original, err := object.ID()
		if err != nil {
			return nil, false, err
		}
		object, err = scope.Apply(ctx, srv, object)
		if err != nil {
			return nil, false, err
		}
		updated, err := object.ID()
		if err != nil {
			return nil, false, err
		}
		if updated.EngineResultID() != original.EngineResultID() {
			return updated, true, nil
		}
		return id, false, nil
	}
	if array, ok := input.(resultCallArrayInput); ok {
		items := array.resultCallArrayValues()
		values := make([]any, len(items))
		changed := false
		for i, item := range items {
			value, change, err := scopeInputValue(ctx, srv, scope, item)
			if err != nil {
				return nil, false, err
			}
			values[i], changed = value, changed || change
		}
		return values, changed, nil
	}
	if object, ok := input.(resultCallInputObject); ok {
		fields := object.resultCallInputObjectFields()
		values := make(map[string]any, len(fields))
		changed := false
		for _, field := range fields {
			value, change, err := scopeInputValue(ctx, srv, scope, field.value)
			if err != nil {
				return nil, false, err
			}
			values[field.name], changed = value, changed || change
		}
		return values, changed, nil
	}
	return input.ToLiteral().ToInput(), false, nil
}
