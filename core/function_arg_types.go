package core

import (
	"context"
	"fmt"
	"sync"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
)

type modTypeMemoKey struct {
	moduleID        uint64
	def             *TypeDef
	view            call.View
	checkDirectDeps bool
}

type modTypeMemoEntry struct {
	mu     sync.Mutex
	typ    ModType
	ok     bool
	loaded bool
}

func memoizedModType(ctx context.Context, mod *userMod, def *TypeDef, checkDirectDeps bool) (ModType, bool, error) {
	memo := currentSessionSchemaBuilderMemo(ctx)
	id, err := mod.res.ID()
	if memo == nil || err != nil || id == nil || id.EngineResultID() == 0 {
		return mod.modTypeFor(ctx, def, checkDirectDeps)
	}
	dag, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, false, err
	}
	key := modTypeMemoKey{moduleID: id.EngineResultID(), def: def, view: dag.View, checkDirectDeps: checkDirectDeps}
	memo.mu.Lock()
	if memo.modTypes == nil {
		memo.modTypes = make(map[modTypeMemoKey]*modTypeMemoEntry)
	}
	entry := memo.modTypes[key]
	if entry == nil {
		entry = &modTypeMemoEntry{}
		memo.modTypes[key] = entry
	}
	memo.mu.Unlock()
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.loaded {
		return entry.typ, entry.ok, nil
	}
	typ, ok, err := mod.modTypeFor(ctx, def, checkDirectDeps)
	if err == nil {
		entry.typ, entry.ok, entry.loaded = typ, ok, true
	}
	return typ, ok, err
}

type canonicalArgType struct {
	mu  sync.Mutex
	def *TypeDef
}

// canonicalFunctionArgType resolves an authored argument against its module's
// dependencies. Hundreds of nested calls can install the same module schema;
// reconstructing its immutable type metadata through DagQL for every client
// serves no purpose. Keep this metadata in the session, alongside its schema
// memo, while leaving clients, fields and query roots independent.
func canonicalFunctionArgType(ctx context.Context, mod Mod, authored dagql.ObjectResult[*TypeDef]) (*TypeDef, error) {
	resolve := func() (*TypeDef, error) {
		typ, ok, err := mod.ModTypeFor(ctx, authored.Self(), true)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("type %s is not a dependency of module %s", authored.Self().ToType(), mod.Name())
		}
		def, err := typ.TypeDef(ctx)
		if err != nil {
			return nil, err
		}
		return def.Self(), nil
	}
	memo := currentSessionSchemaBuilderMemo(ctx)
	if memo == nil {
		return resolve()
	}
	moduleID, moduleErr := mod.ModuleResult().ID()
	typeID, typeErr := authored.ID()
	if moduleErr != nil || typeErr != nil || moduleID == nil || typeID == nil || moduleID.EngineResultID() == 0 || typeID.EngineResultID() == 0 {
		return resolve()
	}
	dag, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	// Module identity includes its exact dependency graph; authored type
	// identity includes optionality and any defaults merged before installation.
	key := fmt.Sprintf("%d:%d:%s", moduleID.EngineResultID(), typeID.EngineResultID(), dag.View)
	return memo.canonicalArgType(key, resolve)
}

func (m *SchemaBuilderMemo) canonicalArgType(key string, resolve func() (*TypeDef, error)) (*TypeDef, error) {
	m.mu.Lock()
	if m.canonicalArgTypes == nil {
		m.canonicalArgTypes = make(map[string]*canonicalArgType)
	}
	entry := m.canonicalArgTypes[key]
	if entry == nil {
		entry = &canonicalArgType{}
		m.canonicalArgTypes[key] = entry
	}
	m.mu.Unlock()
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.def != nil {
		return entry.def, nil
	}
	def, err := resolve()
	if err == nil {
		entry.def = def
	}
	return def, err
}
