package core

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql"
)

// TOMLValue is a state carrier for a TOML value.
//
// The canonical value model is held in Data, encoded as JSON, so the typed
// accessors (asString, asInteger, field, ...) behave the same way JSONValue's
// do. Source, when set, holds the original TOML text and is used to preserve
// comments and formatting when editing a document.
type TOMLValue struct {
	// Data is the value model, encoded as JSON for uniform typed access.
	Data []byte

	// Source, when non-nil, is the original TOML text this value was decoded
	// from. It is used to preserve comments, key ordering and formatting when
	// editing. It is only meaningful for top-level table/document values.
	Source []byte
}

var (
	_ dagql.PersistedObject        = (*TOMLValue)(nil)
	_ dagql.PersistedObjectDecoder = (*TOMLValue)(nil)
)

func (*TOMLValue) Type() *ast.Type {
	return &ast.Type{
		NamedType: "TOMLValue",
		NonNull:   true,
	}
}

func (v *TOMLValue) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (dagql.PersistedObjectEncoding, error) {
	_ = ctx
	_ = cache
	if v == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted TOML value: nil TOML value")
	}
	return encodePersistedObjectPayload(v)
}

func (*TOMLValue) DecodePersistedObject(ctx context.Context, dag *dagql.Server, _ uint64, _ *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error) {
	_ = ctx
	_ = dag
	var v TOMLValue
	if err := json.Unmarshal(payload, &v); err != nil {
		return nil, fmt.Errorf("decode persisted TOML value payload: %w", err)
	}
	return &v, nil
}
