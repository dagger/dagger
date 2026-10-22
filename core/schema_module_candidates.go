package core

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/dagql"
)

// schemaModuleCandidateWalk is one module visited by
// installedSchemaModuleCandidates, with how the walk reached it, so a module
// that fails to resolve is named along with the module that holds it.
type schemaModuleCandidateWalk struct {
	mod    Mod
	name   string
	id     uint64
	parent *schemaModuleCandidateWalk
}

// describe names the module, its shared result and how the walk reached it,
// e.g. `"dep" (result 12, dependency of "app" (result 7, served))`.
func (w *schemaModuleCandidateWalk) describe() string {
	via := "served"
	if w.parent != nil {
		via = "dependency of " + w.parent.describe()
	}
	if w.id == 0 {
		return fmt.Sprintf("%q (%s)", w.name, via)
	}
	return fmt.Sprintf("%q (result %d, %s)", w.name, w.id, via)
}

func (q *Query) installedSchemaModuleCandidates(ctx context.Context, cache *dagql.Cache) ([]dagql.SchemaModuleCandidate, error) {
	served, err := q.CurrentServedDeps(ctx)
	if err != nil {
		return nil, fmt.Errorf("served modules for schema recovery: %w", err)
	}
	var queue []*schemaModuleCandidateWalk
	for _, mod := range served.Mods() {
		queue = append(queue, &schemaModuleCandidateWalk{mod: mod})
	}
	seen := map[uint64]bool{}
	var candidates []dagql.SchemaModuleCandidate
	for head := 0; head < len(queue); head++ {
		walk := queue[head]
		mod := walk.mod.ModuleResult()
		value := mod.Self()
		if value == nil || !value.Source.Valid || value.Source.Value.Self() == nil {
			continue
		}
		walk.name = value.Name()
		id, err := cache.PersistedResultID(mod)
		if err != nil {
			return nil, fmt.Errorf("installed module %s: %w", walk.describe(), err)
		}
		walk.id = id
		if seen[id] {
			continue
		}
		seen[id] = true
		scoped, err := ImplementationScopedModule(ctx, mod)
		if err != nil {
			return nil, fmt.Errorf("installed module %s: %w", walk.describe(), err)
		}
		scopedID, err := cache.PersistedResultID(scoped)
		if err != nil {
			return nil, fmt.Errorf("installed module %s: implementation-scoped module: %w", walk.describe(), err)
		}
		candidates = append(candidates, dagql.SchemaModuleCandidate{ModuleResultID: id, ScopedResultID: scopedID})
		for _, dep := range value.Deps.Mods() {
			queue = append(queue, &schemaModuleCandidateWalk{mod: dep, parent: walk})
		}
	}
	return candidates, nil
}
