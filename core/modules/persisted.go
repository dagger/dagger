package modules

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/dagql"
)

// ModuleConfigClient is installed as a schema object from the core schema and
// can be returned by ordinary module calls, so it carries its own persisted
// representation. Its two plain fields are inline data with no references.
type persistedModuleConfigClientPayload struct {
	Generator string `json:"generator"`
	Directory string `json:"directory"`
}

var (
	_ dagql.PersistedObject        = (*ModuleConfigClient)(nil)
	_ dagql.PersistedObjectDecoder = (*ModuleConfigClient)(nil)
)

func (client *ModuleConfigClient) EncodePersistedObject(context.Context, *dagql.PersistEncodeContext) (dagql.PersistedObjectEncoding, error) {
	if client == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted module config client: nil client")
	}
	raw, err := json.Marshal(persistedModuleConfigClientPayload{Generator: client.Generator, Directory: client.Directory})
	if err != nil {
		return dagql.PersistedObjectEncoding{}, err
	}
	return dagql.PersistedObjectEncoding{JSON: raw}, nil
}

func (*ModuleConfigClient) DecodePersistedObject(_ context.Context, _ *dagql.PersistDecodeContext, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedModuleConfigClientPayload
	if err := dagql.UnmarshalLosslessJSON(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted module config client payload: %w", err)
	}
	return &ModuleConfigClient{Generator: persisted.Generator, Directory: persisted.Directory}, nil
}

func init() {
	dagql.RegisterPersistedObjectFamily(dagql.PersistedObjectFamily{
		Name:    "modules.ModuleConfigClient",
		Typed:   (*ModuleConfigClient)(nil),
		Visitor: dagql.PersistedNoReferences{},
	})
}
