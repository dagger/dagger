package core

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/dagql"
	"github.com/vektah/gqlparser/v2/ast"
)

// JSONValue is a simple state carrier for JSON-encoded bytes
type JSONValue struct {
	Data []byte
}

var _ dagql.PersistedObject = (*JSONValue)(nil)
var _ dagql.PersistedObjectDecoder = (*JSONValue)(nil)

func (*JSONValue) Type() *ast.Type {
	return &ast.Type{
		NamedType: "JSONValue",
		NonNull:   true,
	}
}

func (v *JSONValue) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (dagql.PersistedObjectEncoding, error) {
	_ = ctx
	_ = cache
	if v == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted JSON value: nil JSON value")
	}
	return encodePersistedObjectPayload(v)
}

func (*JSONValue) DecodePersistedObject(ctx context.Context, dag *dagql.Server, _ uint64, _ *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error) {
	_ = ctx
	_ = dag
	var v JSONValue
	if err := json.Unmarshal(payload, &v); err != nil {
		return nil, fmt.Errorf("decode persisted JSON value payload: %w", err)
	}
	return &v, nil
}
