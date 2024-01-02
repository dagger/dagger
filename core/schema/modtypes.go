package schema

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
)

// ModType wraps the core TypeDef type with schema specific concerns like ID conversion
// and tracking of the module in which the type was originally defined.
type ModType interface {
	// ConvertFromSDKResult converts a value returned from an SDK into values expected by the server,
	// including conversion of IDs to their "unpacked" objects
	ConvertFromSDKResult(ctx context.Context, value any) (any, error)

	// ConvertToSDKInput converts a value from the server into a value expected by the SDK, which may
	// include converting objects to their IDs
	ConvertToSDKInput(ctx context.Context, value any) (any, error)

	// SourceMod is the module in which this type was originally defined
	SourceMod() Mod

	// The core API TypeDef representation of this type
	TypeDef() *core.TypeDef
}

// PrimitiveType are the basic types like string, int, bool, void, etc.
type PrimitiveType struct {
	kind core.TypeDefKind
}

func (t *PrimitiveType) ConvertFromSDKResult(ctx context.Context, value any) (any, error) {
	return value, nil
}

func (t *PrimitiveType) ConvertToSDKInput(ctx context.Context, value any) (any, error) {
	return value, nil
}

func (t *PrimitiveType) SourceMod() Mod {
	return nil
}

func (t *PrimitiveType) TypeDef() *core.TypeDef {
	return &core.TypeDef{
		Kind: t.kind,
	}
}

type ListType struct {
	underlying ModType
}

func (t *ListType) ConvertFromSDKResult(ctx context.Context, value any) (any, error) {
	if value == nil {
		return nil, nil
	}

	list, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("expected list, got %T", value)
	}
	resultList := make([]any, len(list))
	for i, item := range list {
		var err error
		resultList[i], err = t.underlying.ConvertFromSDKResult(ctx, item)
		if err != nil {
			return nil, err
		}
	}
	return resultList, nil
}

func (t *ListType) ConvertToSDKInput(ctx context.Context, value any) (any, error) {
	if value == nil {
		return nil, nil
	}

	list, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("expected list, got %T", value)
	}
	resultList := make([]any, len(list))
	for i, item := range list {
		var err error
		resultList[i], err = t.underlying.ConvertToSDKInput(ctx, item)
		if err != nil {
			return nil, err
		}
	}
	return resultList, nil
}

func (t *ListType) SourceMod() Mod {
	return t.underlying.SourceMod()
}

func (t *ListType) TypeDef() *core.TypeDef {
	return &core.TypeDef{
		Kind: core.TypeDefKindList,
		AsList: &core.ListTypeDef{
			ElementTypeDef: t.underlying.TypeDef(),
		},
	}
}
