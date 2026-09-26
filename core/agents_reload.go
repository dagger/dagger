package core

import (
	"context"
	"fmt"
	"slices"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/slog"
)

// RecomposeExpertise replaces the selected expertise's owned contributions, preserving
// the state of its tool objects while loading new implementations. Unowned
// prompts and bindings, and contributions from other expertise, are retained.
// Every replacement is recorded as a real selector, including the module-state
// rebind, rather than an in-memory change to an old object's class.
func RecomposeExpertise(ctx context.Context, base dagql.ObjectResult[*LLM], expertise []*Expertise) (dagql.ObjectResult[*LLM], error) {
	acc := base
	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return base, err
	}
	// Clear each module once, before running any entrypoints. Clearing before
	// every entrypoint would erase contributions from earlier ones in the same
	// module; checking tool state before all have run would reject their tools.
	removed := map[string]bool{}
	for _, entry := range expertise {
		owner := entry.OriginalModule().Name()
		if removed[owner] {
			continue
		}
		removed[owner] = true
		if err := srv.Select(ctx, acc, &acc, dagql.Selector{
			Field: "__withoutComposition",
			Args:  []dagql.NamedInput{{Name: "owner", Value: dagql.String(owner)}},
		}); err != nil {
			return base, err
		}
	}
	for _, entry := range expertise {
		next, err := entry.Run(ctx, acc)
		if err != nil {
			return base, fmt.Errorf("recompose agent %q: %w", entry.Name(), err)
		}
		acc = next
	}
	acc, err = preserveRecomposedTools(ctx, srv, base, acc)
	if err != nil {
		return base, err
	}
	acc.Self().WarnToolNameCollisions(ctx)
	return acc, nil
}

// compositionOwnerMatches compares flat module identities. Empty owners are
// always unowned, never a module selected for recomposition.
func compositionOwnerMatches(contribution, owner string) bool {
	return owner != "" && contribution == owner
}

func snapshotBoundTools(m *MCP) []boundTool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.boundTools)
}

func preserveRecomposedTools(ctx context.Context, srv *dagql.Server, previous, candidate dagql.ObjectResult[*LLM]) (dagql.ObjectResult[*LLM], error) {
	oldBindings := snapshotBoundTools(previous.Self().mcp)
	newBindings := snapshotBoundTools(candidate.Self().mcp)
	byType := make(map[string]boundTool, len(newBindings))
	for _, binding := range newBindings {
		byType[binding.typeName()] = binding
	}
	for _, old := range oldBindings {
		next, exists := byType[old.typeName()]
		if !exists {
			return candidate, fmt.Errorf("reload would discard tool state for %q; use a fresh composition to reset it explicitly", old.typeName())
		}
		if old.Owner != "" && next.Owner != "" && old.Owner != next.Owner {
			return candidate, fmt.Errorf("reload would replace tool binding %q owned by other expertise", old.typeName())
		}
		if stableIDDigest(old.id) == stableIDDigest(next.id) && old.Version == next.Version {
			continue
		}
		rebound, err := recomposeToolReceiver(ctx, srv, old, next)
		if err != nil {
			return candidate, fmt.Errorf("reload tool state %q: %w", old.typeName(), err)
		}
		id, err := rebound.ID()
		if err != nil {
			return candidate, err
		}
		if err := srv.Select(ctx, candidate, &candidate, dagql.Selector{
			Field: "withTools",
			Args: []dagql.NamedInput{
				{Name: "object", Value: dagql.NewAnyID(id)},
				{Name: "except", Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(next.Except...))},
				{Name: "version", Value: dagql.Int(next.Version)},
				{Name: "owner", Value: dagql.Opt(dagql.String(next.Owner))},
			},
		}); err != nil {
			return candidate, fmt.Errorf("record reloaded tool state %q: %w", old.typeName(), err)
		}
	}
	return candidate, nil
}

func recomposeToolReceiver(ctx context.Context, srv *dagql.Server, previous, candidate boundTool) (dagql.AnyObjectResult, error) {
	oldObj, err := loadRecomposeBinding(ctx, srv, previous)
	if err != nil {
		return nil, err
	}
	newObj, err := loadRecomposeBinding(ctx, srv, candidate)
	if err != nil {
		return nil, err
	}
	oldState, oldIsModule := dagql.UnwrapAs[*ModuleObject](oldObj)
	newState, newIsModule := dagql.UnwrapAs[*ModuleObject](newObj)
	if !oldIsModule || !newIsModule {
		return nil, fmt.Errorf("cannot reload non-module tool objects; use a fresh composition to replace them explicitly")
	}
	if err := sameToolStateIdentity(oldState, newState); err != nil {
		return nil, err
	}
	if previous.Version != candidate.Version {
		slog.SpanLogger(ctx, InstrumentationLibrary).Warn("resetting tool state: its withTools version changed",
			"object", candidate.typeName(),
			"previousVersion", previous.Version,
			"currentVersion", candidate.Version)
		return newObj, nil
	}
	return RebindModuleObjectState(ctx, srv, newObj, oldObj)
}

// Load only bindings actually replaced by composition. In particular, unrelated
// lazy bindings (which may no longer be reproducible) must stay lazy. Do not
// populate the old LLM's lazy receiver cache while preparing a candidate.
func loadRecomposeBinding(ctx context.Context, srv *dagql.Server, binding boundTool) (dagql.AnyObjectResult, error) {
	obj := binding.object
	if obj == nil {
		var err error
		obj, err = srv.Load(ctx, binding.id)
		if err != nil {
			return nil, fmt.Errorf("load current state for %q: %w", binding.typeName(), err)
		}
	}
	return binding.objType.New(obj)
}

// A binding slot is currently a GraphQL type name. Before transferring state,
// also check installation name and intrinsic module/object identity. Source
// location is deliberately not identity: replacing a remote dependency with a
// local clone (or a fork) is how expertise can fix itself without losing state.
// Equivalent cached implementations may also carry a different source's metadata.
// The selected expertise is trusted code with access to the base conversation;
// preserveRecomposedTools separately prevents claiming another owner's bindings.
func sameToolStateIdentity(previous, candidate *ModuleObject) error {
	oldMod, newMod := previous.Module.Self(), candidate.Module.Self()
	if oldMod.Name() != newMod.Name() || oldMod.OriginalName != newMod.OriginalName ||
		previous.TypeDef.OriginalName != candidate.TypeDef.OriginalName {
		return fmt.Errorf("module or object identity changed")
	}
	return nil
}
