package core

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/naming"
)

// Naming is the engine's identifier naming at an engine version: parsing and
// formatting names with that version's dictionary, and naming the
// declarations of a module that version names (ModuleNaming).
type Naming struct {
	// Version is the API view the naming answers for: a declared version,
	// normalized as a module's is (engine.ModuleAPIViewVersion).
	Version string
}

func (*Naming) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Naming",
		NonNull:   true,
	}
}

func (*Naming) TypeDescription() string {
	return `The engine's identifier naming rules at an engine version.`
}

// NamingFor returns the naming of an engine version, normalized as a module's
// declared version is: its API view, capped at the engine's own
// (engine.ModuleAPIViewVersion). It's idempotent, so the naming of a
// ModuleNaming's version is that ModuleNaming's.
func NamingFor(version string) *Naming {
	return &Naming{Version: engine.ModuleAPIViewVersion(version)}
}

// Dictionary is the naming dictionary of n's version.
func (n *Naming) Dictionary() *naming.Dictionary {
	return naming.DictionaryFor(n.Version)
}

// Module returns the naming of a module named by n's version: under name,
// spelling its main object and namespaced type names after originalName.
func (n *Naming) Module(name, originalName string) *ModuleNaming {
	return &ModuleNaming{
		View:               call.View(n.Version),
		ModuleName:         name,
		ModuleOriginalName: originalName,
	}
}

type persistedNamingPayload struct {
	Version string `json:"version,omitempty"`
}

func (n *Naming) EncodePersistedObject(context.Context, *dagql.PersistEncodeContext) (dagql.PersistedObjectEncoding, error) {
	if n == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted naming: nil naming")
	}
	return encodePersistedObjectPayload(persistedNamingPayload{Version: n.Version})
}

func (*Naming) DecodePersistedObject(_ context.Context, _ *dagql.PersistDecodeContext, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedNamingPayload
	if err := unmarshalPersistedPayload(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted naming payload: %w", err)
	}
	return &Naming{Version: persisted.Version}, nil
}
