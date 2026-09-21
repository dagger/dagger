package core

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/dagql"
)

func (q *Query) installedSchemaModuleCandidates(ctx context.Context, cache *dagql.Cache) ([]dagql.SchemaModuleCandidate, error) {
	served, err := q.CurrentServedDeps(ctx)
	if err != nil {
		return nil, fmt.Errorf("served modules for schema recovery: %w", err)
	}
	queue := served.Mods()
	seen := map[uint64]bool{}
	var candidates []dagql.SchemaModuleCandidate
	for head := 0; head < len(queue); head++ {
		mod := queue[head].ModuleResult()
		value := mod.Self()
		if value == nil || !value.Source.Valid || value.Source.Value.Self() == nil {
			continue
		}
		id, err := cache.PersistedResultID(mod)
		if err != nil {
			return nil, fmt.Errorf("installed module %q: %w", value.Name(), err)
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		scoped, err := ImplementationScopedModule(ctx, mod)
		if err != nil {
			return nil, err
		}
		scopedID, err := cache.PersistedResultID(scoped)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, dagql.SchemaModuleCandidate{ModuleResultID: id, ScopedResultID: scopedID})
		queue = append(queue, value.Deps.Mods()...)
	}
	return candidates, nil
}
