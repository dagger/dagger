package schema

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

func prepareArtifactAddresses(ctx context.Context, items []*core.Artifact, typed bool) ([]*core.Artifact, error) {
	if !core.Supports(ctx, artifactDimensionViewVersion) {
		return items, nil
	}
	catalogs := map[string]map[string]*core.Artifact{}
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
		catalog, ok := catalogs[key]
		if !ok {
			entries, err := unfilteredAddressItems(ctx, item, typed)
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				catalogs[key] = nil
				continue
			}
			catalog = make(map[string]*core.Artifact, len(entries))
			for _, entry := range entries {
				catalog[entry.AddressIdentity()] = entry
			}
			catalogs[key] = catalog
		}
		if entry := catalog[item.AddressIdentity()]; entry != nil && entry.HasAddressPlan(typed) {
			result[i] = item.WithAddressPlan(entry)
		}
	}
	return result, nil
}

func unfilteredAddressItems(ctx context.Context, item *core.Artifact, typed bool) ([]*core.Artifact, error) {
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
	return selectedItems(ctx, selection)
}
