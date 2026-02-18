package core

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/server/resource"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/util/hashutil"
	"github.com/opencontainers/go-digest"
)

// CollectedContent accumulates core object IDs and a rolling content hash
// while walking a module function's return value. IDs are used for resource
// transfer (secrets, sockets, etc.) and the content hash is used to compute
// content-addressed cache keys for Workspace-aware functions.
type CollectedContent struct {
	// IDs maps recipe digest → resource ID for every core object (Directory,
	// File, Container, …) found in the value tree.
	IDs map[digest.Digest]*resource.ID

	// hasher accumulates a rolling hash of all content found in the value
	// tree — both core object content digests and primitive scalar values.
	// Each implementor feeds its contribution directly, avoiding the need
	// to store intermediate values.
	hasher *hashutil.Hasher

	digest digest.Digest
}

func NewCollectedContent() *CollectedContent {
	return &CollectedContent{
		IDs:    map[digest.Digest]*resource.ID{},
		hasher: hashutil.NewHasher(),
	}
}

// Digest computes and finalizes the content digest.
func (content *CollectedContent) Digest() digest.Digest {
	if content.digest != "" {
		return content.digest
	}
	content.digest = digest.Digest(content.hasher.DigestAndClose())
	return content.digest
}

// CollectID collects an ID, indicating whether it came from an unknown field.
func (content *CollectedContent) CollectID(idp call.ID, unknown bool) {
	rid := &resource.ID{
		ID:       idp,
		Optional: unknown, // mark this id as optional, since it's a best-guess attempt
	}
	content.IDs[idp.Digest()] = rid
	// TODO: deeper integration with dagql/cache would be preferable but OutputEquivalentDigest works
	// for current workspace requirements.
	// Deeper integration would allow full equivalence set cache hits on any IDs, whereas this approach
	// is specific to just "content hash" extra digests.
	dgst := rid.OutputEquivalentDigest()
	content.hasher.WithString(string(dgst))
}

// CollectUnknown naively walks a typically JSON-decoded value, e.g. from a
// module object's private field. It attempts to decode any strings it
// encounters into IDs, and hashes everything into the rolling content hash.
//
// It is also used to content encode anything that is known to be JSONable.
func (content *CollectedContent) CollectUnknown(value any) error {
	switch value := value.(type) {
	case []any:
		for i, value := range value {
			if err := content.CollectIndexed(i, func() error {
				return content.CollectUnknown(value)
			}); err != nil {
				return err
			}
		}
		return nil
	case map[string]any:
		for _, k := range slices.Sorted(maps.Keys(value)) {
			if err := content.CollectKeyed(k, func() error {
				return content.CollectUnknown(value[k])
			}); err != nil {
				return err
			}
		}
		return nil
	case string:
		var idp call.ID
		if err := idp.Decode(value); err == nil {
			content.CollectID(idp, true)
			return nil
		} else {
			return content.CollectJSONable(value)
		}
	default:
		return content.CollectJSONable(value)
	}
}

// CollectJSONable content hashes a JSON-marshalble value.
func (content *CollectedContent) CollectJSONable(value any) error {
	bytes, err := json.Marshal(value)
	if err != nil {
		return err
	}
	content.hasher.WithBytes(bytes...)
	return nil
}

// CollectIndexedUnknown hashes an unknown value, preceded by its array index
// and followed by a delimiter.
func (content *CollectedContent) CollectIndexed(i int, value func() error) error {
	content.hasher.WithInt64(int64(i))
	if err := value(); err != nil {
		return err
	}
	content.hasher.WithDelim()
	return nil
}

// CollectKeyedUnknown hashes an unknown value, preceded by its map key and
// followed by a delimiter.
func (content *CollectedContent) CollectKeyed(key string, value func() error) error {
	content.hasher.WithString(key)
	if err := value(); err != nil {
		return err
	}
	content.hasher.WithDelim()
	return nil
}

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

	// CollectContent walks the given value and collects core object IDs and
	// primitive scalar values into the provided CollectedContent. This is
	// used for resource transfer (IDs) and for computing content-addressed
	// cache keys (IDs + Values).
	CollectContent(ctx context.Context, value dagql.AnyResult, content *CollectedContent) error

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

func (t *PrimitiveType) CollectContent(_ context.Context, value dagql.AnyResult, content *CollectedContent) error {
	if value == nil {
		return content.CollectJSONable(nil)
	}
	return content.CollectJSONable(value.Unwrap())
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

func (t *ListType) CollectContent(ctx context.Context, value dagql.AnyResult, content *CollectedContent) error {
	if value == nil {
		return content.CollectJSONable(nil)
	}

	list, ok := value.Unwrap().(dagql.Enumerable)
	if !ok {
		return fmt.Errorf("%T.CollectContent: expected Enumerable, got %T: %#v", t, value, value)
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

		if err := content.CollectIndexed(i, func() error {
			return t.Underlying.CollectContent(ctx, item, content)
		}); err != nil {
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

func (t *NullableType) CollectContent(ctx context.Context, value dagql.AnyResult, content *CollectedContent) error {
	if value == nil {
		return nil
	}
	val, present := value.DerefValue()
	if !present {
		return nil
	}
	return t.Inner.CollectContent(ctx, val, content)
}

func (t *NullableType) SourceMod() Mod {
	return t.Inner.SourceMod()
}

func (t *NullableType) TypeDef() *TypeDef {
	cp := t.InnerDef.Clone()
	cp.Optional = true
	return cp
}
