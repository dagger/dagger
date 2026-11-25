package schema

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
)

// prepareArtifactAddresses plans each item's shortest address in its
// workspace. A plan expands only the collections whose items could change it,
// so addressing an artifact outside any collection discovers no collection,
// and the session's planner expands each collection once.
func prepareArtifactAddresses(ctx context.Context, items []*core.Artifact, typed bool) ([]*core.Artifact, error) {
	if !core.Supports(ctx, artifactDimensionViewVersion) {
		return items, nil
	}
	planners := map[string]*core.ArtifactAddressPlanner{}
	result := make([]*core.Artifact, len(items))
	for i, item := range items {
		result[i] = item
		if item.Workspace.Self() == nil || item.LoadFailure != nil || item.HasAddressPlan(typed) || !item.HasCompleteAddressSchema() {
			continue
		}
		wsID, err := item.Workspace.ID()
		if err != nil {
			return nil, err
		}
		key := fmt.Sprint(wsID.EngineResultID())
		if typed {
			key += ":" + core.ArtifactTypeName(item.TypeName)
		}
		planner, ok := planners[key]
		if !ok {
			planner, err = workspaceAddressPlanner(ctx, item, typed)
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				planner = nil
			}
			planners[key] = planner
		}
		if planner == nil {
			continue
		}
		source, err := planner.Plan(ctx, item)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		if source != nil {
			result[i] = item.WithAddressPlan(source)
		}
	}
	return result, nil
}

// workspaceAddressPlanner plans addresses against item's unfiltered workspace
// selection, or the selection of item's type for a typed address. A
// session's planner for a selection is shared by every address the session
// plans in it, so a selection of many collection items reads each
// collection's listing into it once; the session's cache releases it.
func workspaceAddressPlanner(ctx context.Context, item *core.Artifact, typed bool) (*core.ArtifactAddressPlanner, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	var selection dagql.ObjectResult[*core.Artifacts]
	if err := srv.Select(ctx, item.Workspace, &selection, dagql.Selector{Field: "artifacts"}); err != nil {
		return nil, err
	}
	if typed {
		var selected dagql.ObjectResult[*core.Artifacts]
		if err := srv.Select(ctx, selection, &selected, dagql.Selector{Field: "filterUri", Args: []dagql.NamedInput{{Name: "uri", Value: dagql.String("dag+" + core.ArtifactTypeName(item.TypeName) + "://")}}}); err != nil {
			return nil, err
		}
		selection = selected
	}
	build := func(context.Context) (*core.ArtifactAddressPlanner, error) {
		return core.NewArtifactAddressPlanner(selection, typed)
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return build(ctx)
	}
	clientMD, err := engine.ClientMetadataFromContext(ctx)
	if err != nil || clientMD.SessionID == "" {
		return build(ctx)
	}
	id, err := selection.ID()
	if err != nil {
		return nil, err
	}
	// The arbitrary cache's keys are not scoped by session, so the key is.
	key := fmt.Sprintf("artifact-address-planner:%s:%d:%s", clientMD.SessionID, id.EngineResultID(), srv.View)
	res, err := cache.GetOrInitArbitrary(ctx, clientMD.SessionID, key, func(ctx context.Context) (any, error) {
		return build(ctx)
	})
	if err != nil {
		return nil, err
	}
	planner, ok := res.Value().(*core.ArtifactAddressPlanner)
	if !ok {
		return nil, fmt.Errorf("artifact address planner: unexpected %T", res.Value())
	}
	return planner, nil
}
