package core

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/server/resource"
	"github.com/dagger/dagger/engine/slog"
	"github.com/opencontainers/go-digest"
)

// ModType wraps the core TypeDef type with schema specific concerns like ID conversion
// and tracking of the module in which the type was originally defined.
type ModType interface {
	// ConvertFromSDKResult converts a value returned from an SDK into values
	// expected by the server, including conversion of IDs to their "unpacked"
	// objects
	ConvertFromSDKResult(ctx context.Context, value any) (dagql.AnyResult, error)

	// ConvertToSDKInput converts a value from the server into a value expected
	// by the SDK, which may include converting objects to their IDs
	ConvertToSDKInput(ctx context.Context, value dagql.Typed) (any, error)

	// CollectCoreIDs collects all the call IDs from core objects in the given value, whether
	// it's idable itself or is a list/object containing idable values (recursively)
	CollectCoreIDs(ctx context.Context, value dagql.AnyResult, ids map[digest.Digest]*resource.ID) error

	// SourceMod is the module in which this type was originally defined
	SourceMod() Mod

	// The core API TypeDef representation of this type
	TypeDef() *TypeDef
}

// PrimitiveType are the basic types like string, int, bool, void, etc.
type PrimitiveType struct {
	Def *TypeDef
}

var _ ModType = &PrimitiveType{}

func (t *PrimitiveType) ConvertFromSDKResult(ctx context.Context, value any) (dagql.AnyResult, error) {
	// NB: we lean on the fact that all primitive types are also dagql.Inputs
	input := t.Def.ToInput()
	if value == nil {
		return dagql.NewResultForCurrentID(ctx, input)
	}

	retVal, err := input.Decoder().DecodeInput(value)
	if err != nil {
		return nil, err
	}
	return dagql.NewResultForCurrentID(ctx, retVal)
}

func (t *PrimitiveType) ConvertToSDKInput(ctx context.Context, value dagql.Typed) (any, error) {
	return value, nil
}

func (t *PrimitiveType) CollectCoreIDs(context.Context, dagql.AnyResult, map[digest.Digest]*resource.ID) error {
	return nil
}

func (t *PrimitiveType) SourceMod() Mod {
	return nil
}

func (t *PrimitiveType) TypeDef() *TypeDef {
	return t.Def
}

type ListType struct {
	Elem       *TypeDef
	Underlying ModType
}

var _ ModType = &ListType{}

func (t *ListType) ConvertFromSDKResult(ctx context.Context, value any) (dagql.AnyResult, error) {
	arr := dagql.DynamicResultArrayOutput{
		Elem: t.Elem.ToTyped(),
	}
	if value == nil {
		slog.Debug("ListType.ConvertFromSDKResult: got nil value")
		// return an empty array, _not_ nil
		return dagql.NewResultForCurrentID(ctx, arr)
	}
	list, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("ListType.ConvertFromSDKResult: expected []any, got %T", value)
	}
	arr.Values = make([]dagql.AnyResult, 0, len(list))
	for i, item := range list {
		var err error

		curID := dagql.CurrentID(ctx)
		itemID := curID.SelectNth(i + 1)
		ctx := dagql.ContextWithID(ctx, itemID)

		t, err := t.Underlying.ConvertFromSDKResult(ctx, item)
		if err != nil {
			return nil, err
		}
		arr.Values = append(arr.Values, t)
	}
	return dagql.NewResultForCurrentID(ctx, arr)
}

func (t *ListType) ConvertToSDKInput(ctx context.Context, value dagql.Typed) (any, error) {
	if value == nil {
		return nil, nil
	}
	list, ok := value.(dagql.Enumerable)
	if !ok {
		return nil, fmt.Errorf("%T.ConvertToSDKInput: expected Enumerable, got %T: %#v", t, value, value)
	}
	resultList := make([]any, list.Len())
	for i := 1; i <= list.Len(); i++ {
		item, err := list.Nth(i)
		if err != nil {
			return nil, err
		}
		resultList[i-1], err = t.Underlying.ConvertToSDKInput(ctx, item)
		if err != nil {
			return nil, err
		}
	}
	return resultList, nil
}

func (t *ListType) CollectCoreIDs(ctx context.Context, value dagql.AnyResult, ids map[digest.Digest]*resource.ID) error {
	if value == nil {
		return nil
	}
	list, ok := value.Unwrap().(dagql.Enumerable)
	if !ok {
		return fmt.Errorf("%T.CollectCoreIDs: expected Enumerable, got %T: %#v", t, value, value)
	}

	for i := 1; i <= list.Len(); i++ {
		item, err := value.NthValue(i)
		if err != nil {
			return err
		}
		if item == nil {
			continue
		}

		ctx := dagql.ContextWithID(ctx, item.ID())

		if err := t.Underlying.CollectCoreIDs(ctx, item, ids); err != nil {
			return err
		}
	}
	return nil
}

func (t *ListType) SourceMod() Mod {
	return t.Underlying.SourceMod()
}

func (t *ListType) TypeDef() *TypeDef {
	return &TypeDef{
		Kind: TypeDefKindList,
		AsList: dagql.NonNull(&ListTypeDef{
			ElementTypeDef: t.Elem.Clone(),
		}),
	}
}

type NullableType struct {
	InnerDef *TypeDef
	Inner    ModType
}

var _ ModType = &NullableType{}

func (t *NullableType) ConvertFromSDKResult(ctx context.Context, value any) (dagql.AnyResult, error) {
	nullable := dagql.DynamicNullable{
		Elem: t.InnerDef.ToTyped(),
	}
	if value != nil {
		val, err := t.Inner.ConvertFromSDKResult(ctx, value)
		if err != nil {
			return nil, err
		}
		nullable.Value = val
		nullable.Valid = true
	}
	return dagql.NewResultForCurrentID(ctx, nullable)
}

func (t *NullableType) ConvertToSDKInput(ctx context.Context, value dagql.Typed) (any, error) {
	if value == nil {
		return nil, nil
	}
	opt, ok := value.(dagql.Derefable)
	if !ok {
		return nil, fmt.Errorf("%T.ConvertToSDKInput: expected Derefable, got %T: %#v", t, value, value)
	}
	val, present := opt.Deref()
	if !present {
		return nil, nil
	}
	result, err := t.Inner.ConvertToSDKInput(ctx, val)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (t *NullableType) CollectCoreIDs(ctx context.Context, value dagql.AnyResult, ids map[digest.Digest]*resource.ID) error {
	if value == nil {
		return nil
	}
	val, present := value.DerefValue()
	if !present {
		return nil
	}
	return t.Inner.CollectCoreIDs(ctx, val, ids)
}

func (t *NullableType) SourceMod() Mod {
	return t.Inner.SourceMod()
}

func (t *NullableType) TypeDef() *TypeDef {
	cp := t.InnerDef.Clone()
	cp.Optional = true
	return cp
}
