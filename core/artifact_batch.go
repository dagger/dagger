package core

import (
	"context"
	"fmt"
	"slices"

	"github.com/dagger/dagger/dagql"
)

func artifactBatchDirective(node *ModTreeNode) string {
	for _, directive := range []string{"check", "generate"} {
		if slices.Contains(node.Directives, directive) {
			return directive
		}
	}
	return ""
}

// Batch replaces selected item checks and generators with matching collection
// operations. Expand must run first: its keys are already intersected with each
// collection, including each distinct parent in nested collections.
func (a *Artifacts) Batch(ctx context.Context) ([]*Artifact, error) {
	type batchGroup struct {
		artifact *Artifact
		receiver *ModTreeNode
	}
	groups := map[string]batchGroup{}
	servers := map[uint64]*dagql.Server{}
	var result []*Artifact
	for _, artifact := range a.Entries {
		planned := artifact.Clone()
		// A generated Changeset's stale check uses the generator's batch too.
		target := planned.Node
		if target != nil && target.Name == "stale" && target.Parent != nil && target.Parent.Type.Self().ToType().Name() == "Changeset" {
			target = target.Parent
		}
		if target == nil || target.Parent == nil || target.Parent.CollectionDimension == nil || artifactBatchDirective(target) == "" {
			result = append(result, artifact)
			continue
		}
		item := target.Parent
		collection := item.Parent
		if item.Name != "batch" {
			id, err := collection.Module.ID()
			if err != nil {
				return nil, err
			}
			srv := servers[id.EngineResultID()]
			if srv == nil {
				srv, err = dagqlServerForModule(ctx, collection.Module)
				if err != nil {
					return nil, err
				}
				servers[id.EngineResultID()] = srv
			}
			typ, err := CollectionBatchType(ctx, srv, collection.Type.Self().AsObject.Value)
			if err != nil {
				return nil, err
			}
			if !typ.Valid {
				result = append(result, artifact)
				continue
			}
			receiver := *item
			receiver.Name, receiver.Type, receiver.DagqlServer = "batch", typ.Value, srv
			replacement, err := receiver.Child(ctx, target.Name)
			if err != nil {
				return nil, err
			}
			if replacement == nil || artifactBatchDirective(replacement) != artifactBatchDirective(target) || replacement.Type.Self().ToType().Name() != target.Type.Self().ToType().Name() {
				result = append(result, artifact)
				continue
			}
			*target = *replacement
			item = target.Parent
		}
		if item.CollectionKey == nil {
			return nil, fmt.Errorf("batch selection %q has no item key", item.CollectionDimension.Identifier)
		}
		key := *item.CollectionKey
		item.CollectionKey = nil
		item.CollectionKeys = []string{key}
		// Do not merge different workspaces, paths, or parent item combinations.
		planned.DimensionKeys = slices.DeleteFunc(slices.Clone(planned.DimensionKeys), func(k *ArtifactDimensionKey) bool {
			return k.Dimension == item.CollectionDimension.Identifier
		})
		identity, err := planned.identity()
		if err != nil {
			return nil, err
		}
		group, exists := groups[identity]
		if !exists {
			group = batchGroup{planned, item}
			groups[identity] = group
			result = append(result, planned)
		} else if !slices.Contains(group.receiver.CollectionKeys, key) {
			group.receiver.CollectionKeys = append(group.receiver.CollectionKeys, key)
		} else {
			continue
		}
		group.artifact.DimensionKeys = append(group.artifact.DimensionKeys, &ArtifactDimensionKey{Dimension: item.CollectionDimension.Identifier, Key: key})
	}
	return result, nil
}
