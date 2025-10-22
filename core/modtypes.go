package core

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/server/resource"
	"github.com/opencontainers/go-digest"
)

// ModType wraps the core TypeDef type with schema specific concerns like ID conversion
// and tracking of the module in which the type was originally defined.
type ModType interface {
	// ConvertFromSDKResult converts a value returned from an SDK into a value
	// expected by the server including conversion of IDs to their "unpacked"
	// objects.
	//
	// It returns two values, the first as a conversion to a standard
	// dagql.Typed, with no additional wrapping, and a dagql.AnyResult with all
	// the appropriate result construction.
	ConvertFromSDKResult(ctx context.Context, id *call.ID, value any) (dagql.Typed, dagql.AnyResult, error)

	// ConvertToSDKInput converts a value from the server into a value expected
	// by the SDK, which may include converting objects to their IDs
	ConvertToSDKInput(ctx context.Context, value dagql.Typed) (any, error)

	// CollectCoreIDs collects all the call IDs from core objects in the given value, whether
	// it's idable itself or is a list/object containing idable values (recursively)
	CollectCoreIDs(ctx context.Context, value dagql.Typed, ids map[digest.Digest]*resource.ID) error

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

func (t *PrimitiveType) ConvertFromSDKResult(ctx context.Context, id *call.ID, value any) (typed dagql.Typed, result dagql.AnyResult, err error) {
	// NB: we lean on the fact that all primitive types are also dagql.Inputs
	input := t.Def.ToInput()
	if value != nil {
		input, err = input.Decoder().DecodeInput(value)
		if err != nil {
			return nil, nil, err
		}
	}

	if id != nil {
		result, err = dagql.NewResultForID(input, id)
		if err != nil {
			return nil, nil, err
		}
	}

	return input, result, err
}

func (t *PrimitiveType) ConvertToSDKInput(ctx context.Context, value dagql.Typed) (any, error) {
	return value, nil
}

func (t *PrimitiveType) CollectCoreIDs(context.Context, dagql.Typed, map[digest.Digest]*resource.ID) error {
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

func (t *ListType) ConvertFromSDKResult(ctx context.Context, id *call.ID, value any) (typed dagql.Typed, result dagql.AnyResult, err error) {
	elem := t.Elem.ToTyped()
	types := dagql.DynamicArrayOutput{Elem: elem}
	results := dagql.DynamicResultArrayOutput{Elem: elem}

	if value != nil {
		list, ok := value.([]any)
		if !ok {
			return nil, nil, fmt.Errorf("ListType.ConvertFromSDKResult: expected []any, got %T", value)
		}
		types.Values = make([]dagql.Typed, 0, len(list))
		if id != nil {
			results.Values = make([]dagql.AnyResult, 0, len(list))
		}
		for i, item := range list {
			var err error

			var itemID *call.ID
			if id != nil {
				itemID = id.SelectNth(i + 1)
			}
			itemTyped, itemResult, err := t.Underlying.ConvertFromSDKResult(ctx, itemID, item)
			if err != nil {
				return nil, nil, err
			}
			types.Values = append(types.Values, itemTyped)
			if id != nil {
				results.Values = append(results.Values, itemResult)
			}
		}
	}

	if id != nil {
		result, err = dagql.NewResultForID(results, id)
		if err != nil {
			return nil, nil, err
		}
	}
	return types, result, nil
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

func (t *ListType) CollectCoreIDs(ctx context.Context, value dagql.Typed, ids map[digest.Digest]*resource.ID) error {
	if value == nil {
		return nil
	}

	if result, ok := value.(dagql.AnyResult); ok {
		list, ok := result.Unwrap().(dagql.Enumerable)
		if !ok {
			return fmt.Errorf("%T.CollectCoreIDs: expected Enumerable, got %T: %#v", t, value, value)
		}
		for i := 1; i <= list.Len(); i++ {
			item, err := result.NthValue(i)
			if err != nil {
				return err
			}
			if err := t.Underlying.CollectCoreIDs(ctx, item, ids); err != nil {
				return err
			}
		}
	} else {
		list, ok := value.(dagql.Enumerable)
		if !ok {
			return fmt.Errorf("%T.CollectCoreIDs: expected Enumerable, got %T: %#v", t, value, value)
		}
		for i := 1; i <= list.Len(); i++ {
			item, err := list.Nth(i)
			if err != nil {
				return err
			}
			if err := t.Underlying.CollectCoreIDs(ctx, item, ids); err != nil {
				return err
			}
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

func (t *NullableType) ConvertFromSDKResult(ctx context.Context, id *call.ID, value any) (typed dagql.Typed, result dagql.AnyResult, err error) {
	nullTyped := dagql.DynamicNullable{Elem: t.InnerDef.ToTyped()}
	nullResult := dagql.DynamicNullable{Elem: t.InnerDef.ToTyped()}
	if value != nil {
		val, result, err := t.Inner.ConvertFromSDKResult(ctx, id, value)
		if err != nil {
			return nil, nil, err
		}
		nullTyped.Value, nullTyped.Valid = val, true
		if id != nil {
			nullResult.Value, nullResult.Valid = result, true
		}
	}

	if id != nil {
		result, err = dagql.NewResultForID(nullResult, id)
		if err != nil {
			return nil, nil, err
		}
	}
	return nullTyped, result, nil
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

func (t *NullableType) CollectCoreIDs(ctx context.Context, value dagql.Typed, ids map[digest.Digest]*resource.ID) error {
	if value == nil {
		return nil
	}

	var val dagql.Typed
	var present bool
	if result, ok := value.(dagql.AnyResult); ok {
		val, present = result.DerefValue()
	} else {
		derefable, ok := value.(dagql.Derefable)
		if !ok {
			return fmt.Errorf("%T.CollectCoreIDs: expected Derefable, got %T: %#v", t, value, value)
		}
		val, present = derefable.Deref()
	}
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
