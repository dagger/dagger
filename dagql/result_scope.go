package dagql

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/dagql/call"
)

// ResultScope replaces object references without changing the source results.
type ResultScope interface {
	Apply(context.Context, *Server, AnyObjectResult) (AnyObjectResult, error)
}

// LazyResultScope rewrites a reference without evaluating its producer. The
// returned recipe must retain the scope until the reference is actually loaded.
type LazyResultScope interface {
	ApplyToID(context.Context, *Server, *call.ID) (*call.ID, error)
}

func scopeSelectorInput(ctx context.Context, srv *Server, scope ResultScope, spec InputSpec, input Input) (Input, bool, error) {
	if !spec.LazyRef {
		return scopeInput(ctx, srv, scope, input)
	}
	if optional, ok := input.(resultCallOptionalInput); ok {
		value, valid := optional.resultCallOptionalValue()
		if !valid {
			return input, false, nil
		}
		updated, changed, err := scopeSelectorInput(ctx, srv, scope, spec, value)
		if err != nil || !changed {
			return input, false, err
		}
		converted, err := input.Decoder().DecodeInput(updated.ToLiteral().ToInput())
		return converted, true, err
	}
	idable, ok := UnwrapAs[IDable](input)
	if !ok {
		return nil, false, fmt.Errorf("lazy argument %q is not an ID", spec.Name)
	}
	id, err := idable.ID()
	if err != nil {
		return nil, false, err
	}
	deferred, ok := scope.(LazyResultScope)
	if !ok {
		return nil, false, fmt.Errorf("scope cannot preserve lazy argument %q", spec.Name)
	}
	updated, err := deferred.ApplyToID(ctx, srv, id)
	if err != nil {
		return nil, false, err
	}
	if updated == id || (!id.IsHandle() && !updated.IsHandle() && id.Digest() == updated.Digest()) {
		return input, false, nil
	}
	converted, err := input.Decoder().DecodeInput(updated)
	return converted, true, err
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
