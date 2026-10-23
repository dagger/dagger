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
		keys     map[string]struct{}
	}
	groups := map[string]batchGroup{}
	servers := map[uint64]*dagql.Server{}
	var result []*Artifact
	for _, artifact := range a.Entries {
		planned := artifact.Clone()
		// A Generator's stale check uses its batch too.
		target := planned.Node
		if target != nil && target.Name == "stale" && target.Parent != nil && target.Parent.Type.Self().ToType().Name() == "Generator" {
			target = target.Parent
		}
		if target == nil || target.Parent == nil || target.Parent.CollectionDimension == nil || artifactBatchDirective(target) == "" {
			result = append(result, artifact)
			continue
		}
		item := target.Parent
		if item.Name != "batch" {
			replaced, err := replaceWithBatch(ctx, servers, target)
			if err != nil {
				return nil, err
			}
			if !replaced {
				result = append(result, artifact)
				continue
			}
			item = target.Parent
		}
		if item.CollectionKey == nil {
			return nil, fmt.Errorf("batch selection %q has no item key", item.CollectionDimension.Identifier)
		}
		key := *item.CollectionKey
		item.CollectionKey = nil
		item.CollectionKeys = []string{key}
		// Exclusions apply after expansion, so they can drop items the
		// expansion counted as complete.
		item.CollectionComplete = item.CollectionComplete && len(a.Selector.ExcludedURIs) == 0
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
			group = batchGroup{
				artifact: planned,
				receiver: item,
			}
			groups[identity] = group
			result = append(result, planned)
		} else {
			// Small groups scan their few keys. From 8 keys on, a map avoids
			// comparing each new key against every key already in the group.
			if group.keys == nil {
				if slices.Contains(group.receiver.CollectionKeys, key) {
					continue
				}
				if len(group.receiver.CollectionKeys) >= 8 {
					group.keys = make(map[string]struct{}, 16)
					for _, previous := range group.receiver.CollectionKeys {
						group.keys[previous] = struct{}{}
					}
					groups[identity] = group
				}
			} else if _, exists := group.keys[key]; exists {
				continue
			}
			if group.keys != nil {
				group.keys[key] = struct{}{}
			}
			group.receiver.CollectionKeys = append(group.receiver.CollectionKeys, key)
			group.receiver.CollectionComplete = group.receiver.CollectionComplete && item.CollectionComplete
		}
		group.artifact.DimensionKeys = append(group.artifact.DimensionKeys, &ArtifactDimensionKey{Dimension: item.CollectionDimension.Identifier, Key: key})
	}
	// A batch that runs its whole collection keys fewer dimensions, which
	// can need a longer type key.
	for _, group := range groups {
		group.artifact.TypeKeyName = a.typeKeyName(group.artifact)
	}
	return result, nil
}

// replaceWithBatch points target at the same operation on its collection's
// batch receiver. It reports false when the collection has no batch, or the
// batch has no operation of the same name, directive and type.
func replaceWithBatch(ctx context.Context, servers map[uint64]*dagql.Server, target *ModTreeNode) (bool, error) {
	item := target.Parent
	collection := item.Parent
	id, err := collection.Module.ID()
	if err != nil {
		return false, err
	}
	srv := servers[id.EngineResultID()]
	if srv == nil {
		srv, err = dagqlServerForModule(ctx, collection.Module)
		if err != nil {
			return false, err
		}
		servers[id.EngineResultID()] = srv
	}
	typ, err := CollectionBatchType(ctx, srv, collection.Type.Self().AsObject.Value)
	if err != nil {
		return false, err
	}
	if !typ.Valid {
		return false, nil
	}
	receiver := *item
	receiver.Name, receiver.Type, receiver.DagqlServer = "batch", typ.Value, srv
	replacement, err := receiver.Child(ctx, target.Name)
	if err != nil {
		return false, err
	}
	if replacement == nil || artifactBatchDirective(replacement) != artifactBatchDirective(target) || artifactProjectedTypeName(replacement) != target.Type.Self().ToType().Name() {
		return false, nil
	}
	replacement.Type = target.Type
	*target = *replacement
	return true, nil
}
