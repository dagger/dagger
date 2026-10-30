package core

import (
	"context"
	"fmt"
	"slices"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/vektah/gqlparser/v2/ast"
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
// modules declare a check or generator on both a collection and its items.
// Execute the collection operation once with the selected keys, including
// exclusions, rather than executing both declarations. A generator's stale
// check follows its generator. New modules keep per-item execution.
func (a *Artifacts) EvaluationItems(ctx context.Context) ([]*Artifact, error) {
	expanded, err := a.Expand(ctx)
	if err != nil {
		return nil, err
	}
	return expanded.legacyBatchChecks(ctx)
}

func (a *Artifacts) legacyBatchChecks(ctx context.Context) ([]*Artifact, error) {
	type batchGroup struct {
		artifact *Artifact
		receiver *ModTreeNode
	}
	groups := map[string]batchGroup{}
	var result []*Artifact
	for _, entry := range a.Entries {
		planned, receiver, dimension, key, err := legacyBatchCheck(ctx, entry)
		if err != nil {
			return nil, err
		}
		if planned == nil {
			// A collection operation that matches its item operation is
			// represented by those selected items. No selected items means no
			// batch to run.
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
		group, ok := groups[identity]
		if !ok {
			group = batchGroup{artifact: planned, receiver: receiver}
			groups[identity] = group
			result = append(result, planned)
		}
		if !slices.Contains(group.receiver.LegacyBatchKeys, key) {
			group.receiver.LegacyBatchKeys = append(group.receiver.LegacyBatchKeys, key)
			group.artifact.DimensionKeys = append(group.artifact.DimensionKeys, &ArtifactDimensionKey{Dimension: dimension, Key: key})
		}
	}
	for _, group := range groups {
		group.artifact.TypeKeyName = a.typeKeyName(group.artifact)
	}
	return result, nil
}

// legacyBatchTarget returns the operation an entry runs and its directive. A
// generator's stale check runs its generator, so it batches with it.
func legacyBatchTarget(entry *Artifact) (*ModTreeNode, string) {
	node := entry.Node
	if node == nil || node.Parent == nil {
		return nil, ""
	}
	switch entry.TypeName {
	case "Check":
		// A generator's stale check is either the Generator's own stale field
		// (itself a check) or the generator's node at the stale path.
		if node.Name == "stale" && node.Parent.Parent != nil && slices.Contains(node.Parent.Directives, "generate") {
			return node.Parent, "generate"
		}
		if slices.Contains(node.Directives, "generate") {
			return node, "generate"
		}
		if slices.Contains(node.Directives, "check") {
			return node, "check"
		}
	case "Generator":
		if slices.Contains(node.Directives, "generate") {
			return node, "generate"
		}
	}
	return nil, ""
}

func legacyBatchCheck(ctx context.Context, entry *Artifact) (*Artifact, *ModTreeNode, string, string, error) {
	target, directive := legacyBatchTarget(entry)
	if target == nil || !legacyCollectionChecks(target) {
		return entry, nil, "", "", nil
	}
	item := target.Parent
	if item.CollectionDimension == nil {
		if legacyCollectionHasItemOperation(item, target.Name, directive) {
			return nil, nil, "", "", nil
		}
		return entry, nil, "", "", nil
	}
	collection := item.Parent
	if collection == nil || collection.ObjectType() == nil {
		return entry, nil, "", "", nil
	}
	fn, ok := collection.ObjectType().FunctionByName(target.Name)
	if !ok || !functionHasDirective(fn, directive) {
		return entry, nil, "", "", nil
	}
	replacement, err := collection.Child(ctx, target.Name)
	if err != nil {
		return nil, nil, "", "", err
	}
	if replacement == nil || !slices.Contains(replacement.Directives, directive) {
		return entry, nil, "", "", nil
	}
	replacement = projectArtifactNode(replacement)
	if replacement.Type.Self().ToType().Name() != target.Type.Self().ToType().Name() {
		return entry, nil, "", "", nil
	}
	if item.CollectionKey == nil {
		return nil, nil, "", "", fmt.Errorf("legacy batch %q has no selected key", item.CollectionDimension.Identifier)
	}
	planned := *entry
	// Retain the artifact's full path and selected view. Only its receiver
	// changes from one item to the enclosing collection.
	if target == entry.Node {
		planned.Node = replacement
	} else {
		stale := *entry.Node
		stale.Parent = replacement
		planned.Node = &stale
	}
	result := planned.Clone()
	result.DimensionKeys = slices.DeleteFunc(slices.Clone(entry.DimensionKeys), func(key *ArtifactDimensionKey) bool {
		return key.Dimension == item.CollectionDimension.Identifier
	})
	receiver := result.Node
	if target != entry.Node {
		receiver = receiver.Parent
	}
	return result, receiver, item.CollectionDimension.Identifier, *item.CollectionKey, nil
}

func functionHasDirective(fn *Function, directive string) bool {
	return slices.ContainsFunc(fn.Directives(), func(d *ast.Directive) bool { return d.Name == directive })
}

func legacyCollectionHasItemOperation(collection *ModTreeNode, name, directive string) bool {
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
	return ok && functionHasDirective(fn, directive) && !probe.functionRequiresCallerArgs(fn)
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
