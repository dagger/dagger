package core

import (
	"context"
	"fmt"
	"slices"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
)

// TODO: Remove legacy collection check batching after modules have migrated to
// per-item checks. Keep the version gate until that compatibility is retired.
func legacyCollectionChecks(node *ModTreeNode) bool {
	mod := node.OriginalModule.Self()
	if mod == nil {
		mod = node.Module.Self()
	}
	if mod == nil {
		return false
	}
	var version string
	if mod.SourceOverrides != nil {
		version = mod.SourceOverrides.EngineVersion
	} else if mod.Source.Valid {
		version = mod.Source.Value.Self().EngineVersion
	} else {
		return false
	}
	return !engine.APIVersionAtLeast(version, engine.CollectionItemChecksVersion)
}

// EvaluationItems keeps listing independent of execution planning. Older
// modules declare a check on both a collection and its items. Execute the
// collection check once with the selected keys, including exclusions, rather
// than executing both declarations. New modules keep per-item execution.
func (a *Artifacts) EvaluationItems(ctx context.Context) ([]*Artifact, error) {
	expanded, err := a.Expand(ctx)
	if err != nil {
		return nil, err
	}
	return expanded.legacyBatchChecks(ctx)
}

func (a *Artifacts) legacyBatchChecks(ctx context.Context) ([]*Artifact, error) {
	groups := map[string]*Artifact{}
	var result []*Artifact
	for _, entry := range a.Entries {
		planned, dimension, key, err := legacyBatchCheck(ctx, entry)
		if err != nil {
			return nil, err
		}
		if planned == nil {
			// A collection check that matches its item check is represented by
			// those selected items. No selected items means no batch to run.
			continue
		}
		if dimension == "" {
			result = append(result, planned.Clone())
			continue
		}
		identity, err := planned.identity()
		if err != nil {
			return nil, err
		}
		moduleID, err := planned.Node.Module.ID()
		if err != nil {
			return nil, err
		}
		identity = fmt.Sprintf("%d:%s:%s", moduleID.EngineResultID(), dimension, identity)
		if planned.View != nil {
			identity += ":" + planned.View.key
		}
		if planned.ContextWorkspace.Self() != nil {
			id, err := planned.ContextWorkspace.ID()
			if err != nil {
				return nil, err
			}
			identity += fmt.Sprintf(":context=%d", id.EngineResultID())
		}
		group := groups[identity]
		if group == nil {
			group = planned
			groups[identity] = group
			result = append(result, group)
		}
		if !slices.Contains(group.Node.LegacyBatchKeys, key) {
			group.Node.LegacyBatchKeys = append(group.Node.LegacyBatchKeys, key)
			group.DimensionKeys = append(group.DimensionKeys, &ArtifactDimensionKey{Dimension: dimension, Key: key})
		}
	}
	for _, group := range groups {
		group.TypeKeyName = a.typeKeyName(group)
	}
	return result, nil
}

func legacyBatchCheck(ctx context.Context, entry *Artifact) (*Artifact, string, string, error) {
	node := entry.Node
	if entry.TypeName != "Check" || node == nil || node.Parent == nil ||
		!slices.Contains(node.Directives, "check") || !legacyCollectionChecks(node) {
		return entry, "", "", nil
	}
	item := node.Parent
	if item.CollectionDimension == nil {
		if legacyCollectionHasItemCheck(item, node.Name) {
			return nil, "", "", nil
		}
		return entry, "", "", nil
	}
	collection := item.Parent
	if collection == nil || collection.ObjectType() == nil {
		return entry, "", "", nil
	}
	fn, ok := collection.ObjectType().FunctionByName(node.Name)
	if !ok || !fn.IsCheck {
		return entry, "", "", nil
	}
	replacement, err := collection.Child(ctx, node.Name)
	if err != nil {
		return nil, "", "", err
	}
	if replacement == nil || !slices.Contains(replacement.Directives, "check") ||
		replacement.Type.Self().ToType().Name() != node.Type.Self().ToType().Name() {
		return entry, "", "", nil
	}
	if item.CollectionKey == nil {
		return nil, "", "", fmt.Errorf("legacy check batch %q has no selected key", item.CollectionDimension.Identifier)
	}
	planned := *entry
	// Retain the artifact's full path and selected view. Only its receiver
	// changes from one item to the enclosing collection.
	planned.Node = replacement
	result := planned.Clone()
	result.DimensionKeys = slices.DeleteFunc(slices.Clone(entry.DimensionKeys), func(key *ArtifactDimensionKey) bool {
		return key.Dimension == item.CollectionDimension.Identifier
	})
	return result, item.CollectionDimension.Identifier, *item.CollectionKey, nil
}

func legacyCollectionHasItemCheck(collection *ModTreeNode, name string) bool {
	obj := collection.ObjectType()
	if obj == nil || obj.Collection == nil || !obj.Collection.Enabled {
		return false
	}
	getName := obj.Collection.Get
	if getName == "" {
		getName = "get"
	}
	get, ok := obj.FunctionByName(getName)
	if !ok {
		return false
	}
	item := get.ReturnType
	if full, ok := collection.types[item.Self().ToType().Name()]; ok {
		item = full
	}
	if !item.Self().AsObject.Valid {
		return false
	}
	fn, ok := item.Self().AsObject.Value.Self().FunctionByName(name)
	probe := &ModTreeNode{Parent: &ModTreeNode{Parent: collection, Type: item}}
	return ok && fn.IsCheck && !probe.functionRequiresCallerArgs(fn)
}

func (node *ModTreeNode) legacyBatchReceiver(ctx context.Context, receiver dagql.AnyObjectResult) (dagql.AnyObjectResult, error) {
	if node.LegacyBatchKeys == nil {
		return receiver, nil
	}
	members, err := node.Parent.ObjectType().CollectionMembers()
	if err != nil {
		return nil, err
	}
	if members == nil {
		return nil, fmt.Errorf("legacy batch %q has no collection receiver", node.Name)
	}
	keyType := members.Get.Args[0].Self().TypeDef.Self()
	keys := dagql.DynamicArrayInput{Elem: keyType.ToInput()}
	for _, text := range node.LegacyBatchKeys {
		key, err := collectionInputFromText(keyType, text)
		if err != nil {
			return nil, err
		}
		keys.Values = append(keys.Values, key)
	}
	var subset dagql.AnyObjectResult
	err = node.DagqlServer.Select(ctx, receiver, &subset, dagql.Selector{
		Field: "subset", Args: []dagql.NamedInput{{Name: "keys", Value: keys}},
	})
	return subset, err
}
