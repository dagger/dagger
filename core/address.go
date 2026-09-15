package core

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/dagql"
	"github.com/vektah/gqlparser/v2/ast"
)

type Address struct {
	Value string
}

var _ dagql.PersistedObject = (*Address)(nil)
var _ dagql.PersistedObjectDecoder = (*Address)(nil)

func (*Address) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Address",
		NonNull:   true,
	}
}

func (*Address) TypeDescription() string {
	return `A standardized address to load containers, directories, secrets, and other object types. Address format depends on the type, and is validated at type selection.`
}

func (addr *Address) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (dagql.PersistedObjectEncoding, error) {
	_ = ctx
	_ = cache
	if addr == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted address: nil address")
	}
	return encodePersistedObjectPayload(addr)
}

func (*Address) DecodePersistedObject(ctx context.Context, dag *dagql.Server, _ uint64, _ *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error) {
	_ = ctx
	_ = dag
	var addr Address
	if err := json.Unmarshal(payload, &addr); err != nil {
		return nil, fmt.Errorf("decode persisted address payload: %w", err)
	}
	return &addr, nil
}
