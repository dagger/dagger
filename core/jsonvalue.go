package core

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/dagger/dagger/dagql"
	"github.com/vektah/gqlparser/v2/ast"
)

// JSONValue is a simple state carrier for JSON-encoded bytes
type JSONValue struct {
	Data []byte
	// decoded is immutable derived metadata, built before this value is
	// published. Keeping it with the value avoids parsing an entire catalogue
	// again for each field lookup and makes its retained size visible to cache
	// accounting. It is reconstructed, never persisted.
	decoded *decodedJSONValue
}

type decodedJSONValue struct {
	value any
	err   error
	bytes int64
}

// NewIndexedJSONValue retains a read-only decoded index alongside the original
// bytes. Decode failures are deferred to operations that need decoded values:
// contents must still accept valid JSON numbers beyond float64's range.
func NewIndexedJSONValue(data []byte) *JSONValue {
	v := &JSONValue{Data: slices.Clone(data), decoded: &decodedJSONValue{}}
	v.decoded.err = json.Unmarshal(v.Data, &v.decoded.value)
	v.decoded.bytes = jsonTreeBytes(v.decoded.value)
	return v
}

func (v *JSONValue) decodedValue() (any, error) {
	if v.decoded != nil {
		return v.decoded.value, v.decoded.err
	}
	var value any
	err := json.Unmarshal(v.Data, &value)
	return value, err
}

func (v *JSONValue) FieldNames() ([]string, error) {
	value, err := v.decodedValue()
	if err != nil {
		return nil, err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("value is not an object")
	}
	return slices.Sorted(maps.Keys(object)), nil
}

func (v *JSONValue) FieldAt(path []string) (*JSONValue, error) {
	current, err := v.decodedValue()
	if err != nil {
		return nil, err
	}
	for _, segment := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("can't lookup field '%s' in non-object value", segment)
		}
		var found bool
		current, found = object[segment]
		if !found {
			return nil, fmt.Errorf("no such field: '%s'", segment)
		}
	}
	data, err := json.Marshal(current)
	if err != nil {
		return nil, err
	}
	return &JSONValue{Data: data, decoded: &decodedJSONValue{value: current, bytes: jsonTreeBytes(current)}}, nil
}

// A coarse retained-memory estimate, computed once outside cache locks.
func jsonTreeBytes(value any) int64 {
	switch value := value.(type) {
	case map[string]any:
		size := int64(64 + 64*len(value))
		for key, child := range value {
			size += int64(len(key)) + jsonTreeBytes(child)
		}
		return size
	case []any:
		size := int64(24 + 16*len(value))
		for _, child := range value {
			size += jsonTreeBytes(child)
		}
		return size
	case string:
		return int64(16 + len(value))
	case nil:
		return 0
	default:
		return 16
	}
}

var _ dagql.PersistedObject = (*JSONValue)(nil)
var _ dagql.PersistedObjectDecoder = (*JSONValue)(nil)
var _ dagql.CachePayloadSizer = (*JSONValue)(nil)

func (v *JSONValue) CachePayloadBytes() int64 {
	if v == nil {
		return 0
	}
	bytes := int64(len(v.Data))
	if v.decoded != nil {
		bytes += v.decoded.bytes
	}
	return bytes
}

func (*JSONValue) Type() *ast.Type {
	return &ast.Type{
		NamedType: "JSONValue",
		NonNull:   true,
	}
}

func (v *JSONValue) EncodePersistedObject(ctx context.Context, enc *dagql.PersistEncodeContext) (dagql.PersistedObjectEncoding, error) {
	_ = ctx
	_ = enc
	if v == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted JSON value: nil JSON value")
	}
	return encodePersistedObjectPayload(v)
}

func (*JSONValue) DecodePersistedObject(ctx context.Context, dec *dagql.PersistDecodeContext, payload json.RawMessage) (dagql.Typed, error) {
	_ = ctx
	_ = dec
	var v JSONValue
	if err := json.Unmarshal(payload, &v); err != nil {
		return nil, fmt.Errorf("decode persisted JSON value payload: %w", err)
	}
	return NewIndexedJSONValue(v.Data), nil
}
