package core

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/slog"
)

// Recompose replaces the selected middleware's owned contributions, preserving
// the state of its tool objects while loading new implementations. Unowned
// prompts and bindings, and contributions from other middleware, are retained.
// Every replacement is recorded as a real selector, including the module-state
// rebind, rather than an in-memory change to an old object's class.
func (r *AgentMiddlewareGroup) Recompose(ctx context.Context, base dagql.ObjectResult[*LLM]) (dagql.ObjectResult[*LLM], error) {
	if r.BoundWorkspace.Self() != nil {
		ctx = WorkspaceToContext(ctx, r.BoundWorkspace)
	}
	acc := base
	for _, agent := range r.Agents {
		next, err := runAgentMiddleware(ctx, agent, acc, true)
		if err != nil {
			return base, fmt.Errorf("recompose agent %q: %w", agent.Name(), err)
		}
		acc = next
	}
	warnToolNameCollisions(ctx, acc.Self())
	return acc, nil
}

// compositionOwnerWithin reports whether a contribution belongs to a selected
// scope or one of its nested compositions. Empty owners are always unowned.
func compositionOwnerWithin(contribution, owner string) bool {
	return owner != "" && (contribution == owner || strings.HasPrefix(contribution, owner+"\n"))
}

// Scope ownership on the LLM value passed across the module boundary, not on
// the Go context: a middleware's nested withTools/withSystemPrompt calls must
// observe the same owner and record it in their results. Restore any enclosing
// scope after a nested composition finishes.
func runAgentMiddleware(ctx context.Context, agent *AgentMiddleware, base dagql.ObjectResult[*LLM], reload bool) (dagql.ObjectResult[*LLM], error) {
	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return base, err
	}
	mod := agent.Node.OriginalModule.Self()
	origin, err := toolStateModuleOrigin(ctx, mod)
	if err != nil {
		return base, fmt.Errorf("middleware owner: %w", err)
	}
	owner := fmt.Sprintf("%q/%q/%q", origin, mod.Name(), agent.Node.PathString())
	// Keep causal ownership: composing Inner through Outer is distinct from
	// composing Inner independently. Quoted identity components cannot contain
	// literal newlines, so this separator unambiguously delimits scope levels.
	// Clearing Outer also clears descendants it no longer composes; retained
	// descendant tool state is restored by Outer's preserveRecomposedTools.
	if parent := base.Self().CompositionOwner(); parent != "" {
		owner = parent + "\n" + owner
	}
	scoped := base
	if reload {
		if err := srv.Select(ctx, scoped, &scoped, dagql.Selector{
			Field: "__withoutComposition",
			Args:  []dagql.NamedInput{{Name: "owner", Value: dagql.String(owner)}},
		}); err != nil {
			return base, err
		}
	}
	if err := srv.Select(ctx, scoped, &scoped, dagql.Selector{
		Field: "__withCompositionOwner",
		Args:  []dagql.NamedInput{{Name: "owner", Value: dagql.String(owner)}},
	}); err != nil {
		return base, err
	}
	next, err := agent.Node.RunAgent(ctx, scoped)
	if err != nil {
		return base, err
	}
	if reload && next.Self().CompositionOwner() != owner {
		return base, fmt.Errorf("middleware did not preserve its base LLM composition scope")
	}
	if reload {
		next, err = preserveRecomposedTools(ctx, srv, base, next, owner)
		if err != nil {
			return base, err
		}
	}
	if err := srv.Select(ctx, next, &next, dagql.Selector{
		Field: "__withCompositionOwner",
		Args:  []dagql.NamedInput{{Name: "owner", Value: dagql.String(base.Self().CompositionOwner())}},
	}); err != nil {
		return base, err
	}
	return next, nil
}

func snapshotBoundTools(m *MCP) []boundTool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.boundTools)
}

func preserveRecomposedTools(ctx context.Context, srv *dagql.Server, previous, candidate dagql.ObjectResult[*LLM], owner string) (dagql.ObjectResult[*LLM], error) {
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
		if old.Owner != "" && !compositionOwnerWithin(old.Owner, owner) && compositionOwnerWithin(next.Owner, owner) {
			return candidate, fmt.Errorf("reload would replace tool binding %q owned by another middleware", old.typeName())
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
	if err := sameToolStateOrigin(ctx, oldState, newState); err != nil {
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
// also check installation name, intrinsic type and source lineage. A new commit
// or workspace overlay is the same lineage; an unrelated source installed under
// the same alias is not. This prevents accidental migration of capabilities
// such as Staff's worker handles; selected middleware itself remains trusted
// code with access to the base conversation.
func sameToolStateOrigin(ctx context.Context, previous, candidate *ModuleObject) error {
	oldMod, newMod := previous.Module.Self(), candidate.Module.Self()
	if oldMod.Name() != newMod.Name() || oldMod.OriginalName != newMod.OriginalName ||
		previous.TypeDef.OriginalName != candidate.TypeDef.OriginalName {
		return fmt.Errorf("module or object identity changed")
	}
	oldOrigin, err := toolStateModuleOrigin(ctx, oldMod)
	if err != nil {
		return err
	}
	newOrigin, err := toolStateModuleOrigin(ctx, newMod)
	if err != nil {
		return err
	}
	if oldOrigin != newOrigin {
		return fmt.Errorf("module source changed from %q to %q; refusing to transfer state", oldOrigin, newOrigin)
	}
	return nil
}

func toolStateModuleOrigin(ctx context.Context, mod *Module) (string, error) {
	if !mod.Source.Valid || mod.Source.Value.Self() == nil {
		return "", fmt.Errorf("module %q has no source identity", mod.Name())
	}
	src := mod.Source.Value.Self()
	if src.Kind == ModuleSourceKindGit && src.Git != nil {
		return "git:" + GitRefString(src.Git.CloneRef, src.SourceRootSubpath, ""), nil
	}
	// Native local sources use the same origin whether loaded by the session's
	// module registry or through a Workspace overlay. The former need not carry
	// a Workspace value at all.
	if src.Kind == ModuleSourceKindLocal && src.Local != nil {
		return "local:" + filepath.Join(src.Local.ContextDirectoryPath, src.SourceRootSubpath), nil
	}
	if ws := src.Workspace.Self(); ws != nil {
		return fmt.Sprintf("workspace:%s:%s:%s", ws.Address, ws.selectedEnv, src.SourceRootSubpath), nil
	}
	if src.Kind == ModuleSourceKindDir && src.DirSrc != nil && src.DirSrc.OriginalContextDir.Self() != nil {
		id, err := src.DirSrc.OriginalContextDir.RecipeDigest(ctx)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("directory:%s:%s", id, src.SourceRootSubpath), nil
	}
	return "", fmt.Errorf("module %q has no stable source identity", mod.Name())
}
