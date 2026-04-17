package core

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/util/hashutil"
	"github.com/opencontainers/go-digest"
)

// CollectedContent accumulates a rolling content hash while walking a module
// function's value tree. The content hash is used to compute content-addressed
// cache keys for Workspace-aware functions.
type CollectedContent struct {
	// hasher accumulates a rolling hash of all content found in the value
	// tree — both core object content digests and primitive scalar values.
	// Each implementor feeds its contribution directly, avoiding the need
	// to store intermediate values.
	hasher *hashutil.Hasher

	digest digest.Digest
}

func NewCollectedContent() *CollectedContent {
	return &CollectedContent{
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
func (content *CollectedContent) CollectID(ctx context.Context, idp *call.ID, unknown bool) error {
	if idp == nil {
		return nil
	}
	if collectedContentInvalidID(idp) {
		if unknown {
			// Unknown/private fields are collected on a best-effort basis only.
			// Some SDK paths can surface zero-value IDs for unset optional fields;
			// treat those as absent rather than crashing the engine.
			if err := content.CollectJSONable(nil); err != nil {
				return fmt.Errorf("collect id unknown-invalid-id fallback jsonable nil: %w", err)
			}
			return nil
		}
		return fmt.Errorf("invalid ID")
	}
	// TODO: deeper integration with dagql/cache would be preferable but ContentPreferredDigest works
	// for current workspace requirements.
	// Deeper integration would allow full equivalence set cache hits on any IDs, whereas this approach
	// is specific to just "content hash" extra digests.
	var dgst digest.Digest
	if idp.IsHandle() {
		dag := dagql.CurrentDagqlServer(ctx)
		if dag == nil {
			return fmt.Errorf("current dagql server is nil")
		}
		res, err := dag.LoadType(ctx, idp)
		if err != nil {
			return fmt.Errorf("load handle type: %w", err)
		}
		call, err := res.ResultCall()
		if err != nil {
			return fmt.Errorf("result call: %w", err)
		}
		dgst, err = call.ContentPreferredDigest(ctx)
		if err != nil {
			return fmt.Errorf("content preferred digest: %w", err)
		}
	} else {
		dgst = idp.ContentPreferredDigest()
	}
	content.hasher.WithString(string(dgst))
	return nil
}

func collectedContentInvalidID(idp *call.ID) bool {
	if idp == nil {
		return true
	}
	if idp.IsHandle() {
		return idp.EngineResultID() == 0 || idp.Type() == nil
	}
	return idp.Call() == nil
}

// CollectUnknown naively walks a typically JSON-decoded value, e.g. from a
// module object's private field. It attempts to decode any strings it
// encounters into IDs, and hashes everything into the rolling content hash.
//
// It is also used to content encode anything that is known to be JSONable.
func (content *CollectedContent) CollectUnknown(ctx context.Context, value any) error {
	switch value := value.(type) {
	case dagql.AnyResult:
		id, err := value.ID()
		if err != nil {
			return err
		}
		return content.CollectID(ctx, id, true)
	case dagql.IDable:
		id, err := value.ID()
		if err != nil {
			return err
		}
		return content.CollectID(ctx, id, true)
	case *call.ID:
		return content.CollectID(ctx, value, true)
	case call.ID:
		return content.CollectID(ctx, &value, true)
	case []any:
		for i, value := range value {
			if err := content.CollectIndexed(i, func() error {
				return content.CollectUnknown(ctx, value)
			}); err != nil {
				return err
			}
		}
		return nil
	case map[string]any:
		for _, k := range slices.Sorted(maps.Keys(value)) {
			if err := content.CollectKeyed(k, func() error {
				return content.CollectUnknown(ctx, value[k])
			}); err != nil {
				return err
			}
		}
		return nil
	case string:
		var idp call.ID
		if err := idp.Decode(value); err == nil {
			return content.CollectID(ctx, &idp, true)
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
		return fmt.Errorf("json marshal %T: %w", value, err)
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

	// CollectContent walks the given value and hashes core object content and
	// primitive scalar values into the provided CollectedContent.
	CollectContent(ctx context.Context, value dagql.AnyResult, content *CollectedContent) error

	// SourceMod is the module in which this type was originally defined
	SourceMod() Mod

	// The core API TypeDef representation of this type
	TypeDef(context.Context) (dagql.ObjectResult[*TypeDef], error)
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
		return dagql.NewResultForCurrentCall(ctx, input)
	}

	retVal, err := input.Decoder().DecodeInput(value)
	if err != nil {
		return nil, err
	}
	return dagql.NewResultForCurrentCall(ctx, retVal)
}

func (t *PrimitiveType) ConvertToSDKInput(ctx context.Context, value dagql.Typed) (any, error) {
	return value, nil
}

func (t *PrimitiveType) CollectContent(ctx context.Context, value dagql.AnyResult, content *CollectedContent) error {
	if value == nil {
		return content.CollectJSONable(nil)
	}
	return content.CollectJSONable(value.Unwrap())
}

func (t *PrimitiveType) SourceMod() Mod {
	return nil
}

func (t *PrimitiveType) TypeDef(ctx context.Context) (dagql.ObjectResult[*TypeDef], error) {
	dag, err := CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*TypeDef]{}, err
	}
	var inst dagql.ObjectResult[*TypeDef]
	if err := dag.Select(ctx, dag.Root(), &inst,
		dagql.Selector{Field: "typeDef"},
		dagql.Selector{
			Field: "withKind",
			Args: []dagql.NamedInput{
				{Name: "kind", Value: t.Def.Kind},
			},
		},
	); err != nil {
		return inst, err
	}
	if t.Def.Optional {
		if err := dag.Select(ctx, inst, &inst, dagql.Selector{
			Field: "withOptional",
			Args:  []dagql.NamedInput{{Name: "optional", Value: dagql.Boolean(true)}},
		}); err != nil {
			return inst, err
		}
	}
	return inst, nil
}

type ListType struct {
	Elem       dagql.ObjectResult[*TypeDef]
	Underlying ModType
}

var _ ModType = &ListType{}

func (t *ListType) ConvertFromSDKResult(ctx context.Context, value any) (dagql.AnyResult, error) {
	arr := dagql.DynamicResultArrayOutput{
		Elem: t.Elem.Self().ToTyped(),
	}
	if value == nil {
		slog.Debug("ListType.ConvertFromSDKResult: got nil value")
		// return an empty array, _not_ nil
		return dagql.NewResultForCurrentCall(ctx, arr)
	}
	list, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("ListType.ConvertFromSDKResult: expected []any, got %T", value)
	}
	arr.Values = make([]dagql.AnyResult, 0, len(list))
	for i, item := range list {
		var err error

		itemCtx := ctx
		if curCall := dagql.CurrentCall(ctx); curCall != nil {
			itemCall := cloneResultCall(curCall)
			itemCall.Nth = int64(i + 1)
			if itemCall.Type != nil {
				itemCall.Type = itemCall.Type.Elem
			}
			itemCtx = dagql.ContextWithCall(ctx, itemCall)
		}

		t, err := t.Underlying.ConvertFromSDKResult(itemCtx, item)
		if err != nil {
			return nil, err
		}
		arr.Values = append(arr.Values, t)
	}
	return dagql.NewResultForCurrentCall(ctx, arr)
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
		item, err := value.NthValue(ctx, i)
		if err != nil {
			return err
		}
		if item == nil {
			continue
		}

		itemCtx := ctx
		itemCall, err := item.ResultCall()
		if err != nil {
			return err
		}
		if itemCall != nil {
			itemCtx = dagql.ContextWithCall(ctx, itemCall)
		}

		if err := content.CollectIndexed(i, func() error {
			return t.Underlying.CollectContent(itemCtx, item, content)
		}); err != nil {
			return err
		}
	}
	return nil
}

func (t *ListType) SourceMod() Mod {
	return t.Underlying.SourceMod()
}

func (t *ListType) TypeDef(ctx context.Context) (dagql.ObjectResult[*TypeDef], error) {
	dag, err := CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*TypeDef]{}, err
	}
	elemID, err := t.Elem.ID()
	if err != nil {
		return dagql.ObjectResult[*TypeDef]{}, err
	}
	var inst dagql.ObjectResult[*TypeDef]
	if err := dag.Select(ctx, dag.Root(), &inst,
		dagql.Selector{Field: "typeDef"},
		dagql.Selector{
			Field: "withListOf",
			Args: []dagql.NamedInput{
				{Name: "elementType", Value: dagql.NewID[*TypeDef](elemID)},
			},
		},
	); err != nil {
		return inst, err
	}
	return inst, nil
}

type NullableType struct {
	InnerDef dagql.ObjectResult[*TypeDef]
	Inner    ModType
}

var _ ModType = &NullableType{}

func (t *NullableType) ConvertFromSDKResult(ctx context.Context, value any) (dagql.AnyResult, error) {
	if value != nil {
		val, err := t.Inner.ConvertFromSDKResult(ctx, value)
		if err != nil {
			return nil, err
		}
		return val.NullableWrapped(), nil
	}
	return dagql.NewResultForCurrentCall(ctx, dagql.DynamicNullable{
		Elem: t.InnerDef.Self().ToTyped(),
	})
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

func (t *NullableType) TypeDef(ctx context.Context) (dagql.ObjectResult[*TypeDef], error) {
	dag, err := CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*TypeDef]{}, err
	}
	inst := t.InnerDef
	if err := dag.Select(ctx, inst, &inst, dagql.Selector{
		Field: "withOptional",
		Args:  []dagql.NamedInput{{Name: "optional", Value: dagql.Boolean(true)}},
	}); err != nil {
		return inst, err
	}
	return inst, nil
}
