package core

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/dagger/dagger/core/artifact"
	"github.com/dagger/dagger/core/dagaddress"
	"github.com/dagger/dagger/dagql"
	"golang.org/x/sync/errgroup"
)

type ArtifactDimension = artifact.Dimension

func (a *Artifact) dimensionName(identifier string) string {
	if name := a.DimensionNames[identifier]; name != "" {
		return name
	}
	return identifier
}

func collectionInputFromText(typ *TypeDef, text string) (dagql.Input, error) {
	var raw any = text
	switch typ.Kind {
	case TypeDefKindInteger, TypeDefKindFloat, TypeDefKindBoolean:
		dec := json.NewDecoder(strings.NewReader(text))
		dec.UseNumber()
		if err := dec.Decode(&raw); err != nil {
			return nil, fmt.Errorf("invalid %s collection key %q: %w", typ.ToType(), text, err)
		}
	}
	return typ.ToInput().Decoder().DecodeInput(raw)
}

func (a *Artifact) DimensionDefinitions() []*ArtifactDimension {
	var dims []*ArtifactDimension
	for node := a.Node; node != nil; node = node.Parent {
		if node.CollectionDimension != nil {
			dims = append(dims, node.CollectionDimension)
		}
	}
	slices.Reverse(dims)
	if a.TypeName != "" {
		dims = append(dims, &ArtifactDimension{Kind: "TYPE", Identifier: "type:" + a.TypeName, Name: ArtifactTypeName(a.TypeName), QualifiedName: "artifact-" + ArtifactTypeName(a.TypeName), ItemType: a.TypeName, KeyName: "name"})
	}
	return dims
}

func (a *Artifacts) DimensionDefinitions() artifact.Dimensions {
	dims := map[string]*ArtifactDimension{}
	for _, artifact := range a.Entries {
		for _, dim := range artifact.DimensionDefinitions() {
			dims[dim.Identifier] = dim
		}
	}
	result := make([]*ArtifactDimension, 0, len(dims))
	for _, id := range slices.Sorted(maps.Keys(dims)) {
		result = append(result, dims[id])
	}
	return result
}

// BindDimensions resolves names using the selected schema paths only. This is
// delayed until a result is requested, so filter order cannot bind an alias to
// a dimension that happened to have matching runtime keys.
func (a *Artifacts) BindDimensions() (*Artifacts, error) {
	bound := a.filter(func(*Artifact) bool { return true })
	bound.Selector.Dimensions = nil
	bound.Selector.DimensionAlternatives = nil
	dims := a.DimensionDefinitions()
	for _, filter := range a.Selector.Dimensions {
		id, err := dims.Resolve(filter.Dimension)
		if err != nil {
			return nil, err
		}
		bound.Selector.addDimension(ArtifactDimensionFilter{Dimension: id, Keys: filter.Keys})
	}
	for _, group := range a.Selector.DimensionAlternatives {
		ids := []string{}
		for _, name := range group {
			id, err := dims.Resolve(name)
			if err != nil {
				return nil, err
			}
			if slices.ContainsFunc(dims, func(d *ArtifactDimension) bool { return d.Identifier == id }) && !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
		if len(ids) == 1 {
			bound.Selector.addDimension(ArtifactDimensionFilter{Dimension: ids[0]})
		} else {
			bound.Selector.DimensionAlternatives = append(bound.Selector.DimensionAlternatives, ids)
		}
	}
	return bound, nil
}

func (a *Artifacts) ResolveDimension(name string) (string, error) {
	return a.DimensionDefinitions().Resolve(name)
}

func (a *Artifacts) hasCollections() bool {
	return slices.ContainsFunc(a.DimensionDefinitions(), func(d *ArtifactDimension) bool { return d.Kind != "TYPE" })
}

func (a *Artifacts) matchesDimensionFilters(dims []*ArtifactDimension) bool {
	for _, filter := range a.Selector.Dimensions {
		if filter.Keys != nil && len(filter.Keys) == 0 {
			return false
		}
		if !slices.ContainsFunc(dims, func(d *ArtifactDimension) bool { return d.Identifier == filter.Dimension }) {
			return false
		}
	}
	for _, group := range a.Selector.DimensionAlternatives {
		if !slices.ContainsFunc(dims, func(d *ArtifactDimension) bool { return slices.Contains(group, d.Identifier) }) {
			return false
		}
	}
	return true
}

// ForDimensionKeys avoids expanding descendants when an ancestor artifact
// already supplies every key they could contribute. Keep descendants when
// their dimensions are needed to satisfy a filter.
func (a *Artifacts) ForDimensionKeys(dimension string) *Artifacts {
	if !a.hasCollections() || len(a.Selector.ExcludedURIs) > 0 {
		return a
	}
	return a.filter(func(candidate *Artifact) bool {
		childDims := collectionDimensions(candidate)
		for _, ancestor := range a.Entries {
			parentDims := collectionDimensions(ancestor)
			if len(ancestor.Path) > len(candidate.Path) || len(parentDims) > len(childDims) ||
				len(ancestor.Path) == len(candidate.Path) && len(parentDims) == len(childDims) {
				continue
			}
			if !slices.Equal(ancestor.Path, candidate.Path[:len(ancestor.Path)]) || !a.matchesDimensionFilters(parentDims) ||
				!slices.ContainsFunc(parentDims, func(d *ArtifactDimension) bool { return d.Identifier == dimension }) {
				continue
			}
			if slices.EqualFunc(parentDims, childDims[:len(parentDims)], func(a, b *ArtifactDimension) bool { return a.Identifier == b.Identifier }) {
				return false
			}
		}
		return true
	})
}

// DimensionItems projects expanded artifacts to the collection item that
// supplies dimension. Parent keys remain attached; descendant keys are removed.
func (a *Artifacts) DimensionItems(dimension string) ([]*Artifact, error) {
	if strings.HasPrefix(dimension, "type:") {
		return nil, fmt.Errorf("dimensionItems requires a collection dimension, got %q", dimension)
	}
	items := []*Artifact{}
	seen := map[string]bool{}
	for _, selected := range a.Entries {
		for node := selected.Node; node != nil; node = node.Parent {
			if node.CollectionDimension == nil || node.CollectionDimension.Identifier != dimension {
				continue
			}
			members, err := node.Parent.ObjectType().CollectionMembers()
			if err != nil {
				return nil, err
			}
			// A batch-only operation carries the same dimension on its batch
			// receiver. Project it to get(key), never to the batch object.
			itemNode := *node
			itemNode.Name = "get"
			itemNode.Type = members.Get.ReturnType
			itemNode.Description = members.Get.Description
			itemNode.Directives = nil
			itemNode.CollectionKeys = nil
			item := *selected
			item.Node = &itemNode
			item.Path = itemNode.CommandPath().CliCase()
			if len(item.Path) == 0 {
				item.Path = itemNode.Path().CliCase()
			}
			item.TypeName = itemNode.ObjectType().Name
			item.Directives = nil
			item.DimensionKeys = nil
			for _, key := range selected.DimensionKeys {
				item.DimensionKeys = append(item.DimensionKeys, key)
				if key.Dimension == dimension {
					break
				}
			}
			item.setTypeDimension()
			id, err := item.identity()
			if err != nil {
				return nil, err
			}
			if !seen[id] {
				items = append(items, item.Clone())
				seen[id] = true
			}
			break
		}
	}
	return items, nil
}

// Expand evaluates only the collection receivers needed to enumerate selected
// keys. A leaf artifact's value remains deferred.
func (a *Artifacts) Expand(ctx context.Context) (*Artifacts, error) {
	return a.expand(ctx, artifactCollectionKeys)
}

// artifactCollectionKeyFunc evaluates a collection receiver, never the item or leaf.
type artifactCollectionKeyFunc func(context.Context, *Artifact) ([]collectionKey, error)

func artifactCollectionKeys(ctx context.Context, artifact *Artifact) ([]collectionKey, error) {
	var value dagql.AnyResult
	if err := artifact.Evaluate(ctx, &value); err != nil {
		return nil, err
	}
	obj, ok := dagql.UnwrapAs[*ModuleObject](value)
	if !ok {
		return nil, fmt.Errorf("returned %T instead of a collection", value.Unwrap())
	}
	return obj.collectionKeys(ctx)
}

func (a *Artifacts) expand(ctx context.Context, collectionKeys artifactCollectionKeyFunc) (*Artifacts, error) {
	if len(a.Selector.ExcludedURIs) > 0 {
		included := a.filter(func(*Artifact) bool { return true })
		included.Selector.ExcludedURIs = nil
		result, err := included.expand(ctx, collectionKeys)
		if err != nil {
			return nil, err
		}
		for _, uri := range a.Selector.ExcludedURIs {
			address, err := dagaddress.Parse(uri)
			if err != nil {
				return nil, err
			}
			// Match the exclusion with its own selector. The entries already
			// satisfy the inclusion filters.
			excluded, err := (&Artifacts{Entries: result.Entries}).FilterURI(address)
			if err != nil {
				return nil, err
			}
			excluded, err = excluded.expand(ctx, collectionKeys)
			if err != nil {
				return nil, err
			}
			identities := map[string]bool{}
			for _, artifact := range excluded.Entries {
				identities[artifactIdentity(artifact)] = true
			}
			result = result.filter(func(artifact *Artifact) bool { return !identities[artifactIdentity(artifact)] })
		}
		result.Selector.ExcludedURIs = slices.Clone(a.Selector.ExcludedURIs)
		return result, nil
	}
	bound, err := a.SchemaSelection()
	if err != nil {
		return nil, err
	}
	if !bound.hasCollections() {
		for _, item := range bound.Entries {
			item.setTypeDimension()
		}
		return bound, nil
	}
	result := &Artifacts{Entries: []*Artifact{}, Selector: bound.Selector}
	// Each worker owns one slot; flatten only after all workers finish so
	// discovery order is independent of evaluation completion order.
	entries := make([][]*Artifact, len(bound.Entries))
	group, ctx := errgroup.WithContext(ctx)
	for i, template := range bound.Entries {
		dims := template.DimensionDefinitions()
		if !bound.matchesDimensionFilters(dims) {
			continue
		}
		// An exact collection path without its dimension selects the collection.
		if template.Node != nil && template.Node.CollectionDimension != nil && bound.hasExactPath(template.Path) && !bound.selectsDimension(template.Node.CollectionDimension.Identifier) {
			continue
		}
		group.Go(func() error {
			nodes, err := expandArtifactNode(ctx, template.Node, template.Workspace, bound.Selector.Dimensions, collectionKeys)
			if err != nil {
				return err
			}
			dimensionNames := map[string]string{}
			pathDims := bound.FilterPath(template.Path).DimensionDefinitions()
			for _, dim := range dims {
				dimensionNames[dim.Identifier] = pathDims.DisplayName(dim)
			}
			for _, node := range nodes {
				item := template.Clone()
				item.Node = node
				item.DimensionKeys = []*ArtifactDimensionKey{}
				for n := node; n != nil; n = n.Parent {
					if n.CollectionDimension != nil && n.CollectionKey != nil {
						item.DimensionKeys = append(item.DimensionKeys, &ArtifactDimensionKey{Dimension: n.CollectionDimension.Identifier, Key: *n.CollectionKey})
					}
				}
				slices.Reverse(item.DimensionKeys)
				item.setTypeDimension()
				item.DimensionNames = dimensionNames
				entries[i] = append(entries[i], item)
			}
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}
	for _, items := range entries {
		result.Entries = append(result.Entries, items...)
	}
	return result, nil
}

func artifactIdentity(artifact *Artifact) string {
	// Identity uses exact dimension identifiers, independent of display aliases.
	encoded, _ := json.Marshal(struct {
		Path []string
		Keys []*ArtifactDimensionKey
	}{artifact.Path, artifact.DimensionKeys})
	return string(encoded)
}

func (a *Artifacts) hasExactPath(path []string) bool {
	joined := strings.Join(path, "/")
	return slices.Contains(a.Selector.CollectionPaths, joined)
}

func (a *Artifacts) selectsDimension(id string) bool {
	if slices.ContainsFunc(a.Selector.Dimensions, func(d ArtifactDimensionFilter) bool { return d.Dimension == id }) {
		return true
	}
	return slices.ContainsFunc(a.Selector.DimensionAlternatives, func(group []string) bool { return slices.Contains(group, id) })
}

func expandArtifactNode(ctx context.Context, node *ModTreeNode, ws dagql.ObjectResult[*Workspace], filters []ArtifactDimensionFilter, collectionKeys artifactCollectionKeyFunc) ([]*ModTreeNode, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if node == nil {
		return []*ModTreeNode{nil}, nil
	}
	parents, err := expandArtifactNode(ctx, node.Parent, ws, filters, collectionKeys)
	if err != nil {
		return nil, err
	}
	var expanded []*ModTreeNode
	if node.CollectionDimension == nil || node.CollectionKey != nil {
		if node.CollectionDimension != nil && slices.ContainsFunc(filters, func(filter ArtifactDimensionFilter) bool {
			return filter.Dimension == node.CollectionDimension.Identifier && filter.Keys != nil && !slices.Contains(filter.Keys, *node.CollectionKey)
		}) {
			return nil, nil
		}
		for _, parent := range parents {
			copy := *node
			copy.Parent = parent
			expanded = append(expanded, &copy)
		}
		return expanded, nil
	}

	// Parent items are independent collection receivers. Preserve their order
	// and each receiver's key order, even when they finish out of order.
	children := make([][]*ModTreeNode, len(parents))
	group, ctx := errgroup.WithContext(ctx)
	for i, parent := range parents {
		group.Go(func() error {
			if err := ctx.Err(); err != nil {
				return err
			}
			keys, err := collectionKeys(ctx, &Artifact{Node: parent, Workspace: ws})
			if err != nil {
				return fmt.Errorf("dimension %q: %w", node.CollectionDimension.Identifier, err)
			}
			for _, key := range keys {
				keep := true
				for _, filter := range filters {
					if filter.Dimension == node.CollectionDimension.Identifier && filter.Keys != nil && !slices.Contains(filter.Keys, key.text) {
						keep = false
					}
				}
				if keep {
					copy := *node
					copy.Parent = parent
					copy.CollectionKey = &key.text
					children[i] = append(children[i], &copy)
				}
			}
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}
	for _, nodes := range children {
		expanded = append(expanded, nodes...)
	}
	return expanded, nil
}

func walkArtifactNodes(ctx context.Context, node *ModTreeNode, visit func(*ModTreeNode), visiting map[string]bool) error {
	typ := node.Type.Self()
	if typ == nil || typ.Optional || typ.Kind != TypeDefKindObject {
		return nil
	}
	obj := node.ObjectType()
	if mod := node.OriginalModule.Self(); mod != nil {
		if full, ok := mod.objectTypeDefResultByName(obj.Name); ok {
			node.Type = full
			obj = node.ObjectType()
		}
	}
	// An engine object root can have a synthetic parent for its artifact address.
	if node.Parent != nil && node.RootValue == nil && node.CollectionDimension == nil {
		parent := node.Parent.ObjectType()
		if (parent == nil || parent.SourceModuleName == "") && !slices.ContainsFunc(node.Directives, isArtifactDirective) {
			return nil
		}
	}
	node = projectArtifactNode(node)
	obj = node.ObjectType()
	visit(node)
	if visiting[obj.Name] {
		return nil
	}
	visiting[obj.Name] = true
	defer delete(visiting, obj.Name)
	members, err := obj.CollectionMembers()
	if err != nil {
		return err
	}
	if members != nil {
		parentName, parentOriginal, field := "Query", "Query", gqlFieldName(node.Module.Self().Name())
		if node.Parent != nil && node.Parent.ObjectType() != nil {
			parentName = node.Parent.ObjectType().Name
			parentOriginal = node.Parent.ObjectType().OriginalName
			field = node.Name
		}
		dim := &ArtifactDimension{
			Kind: "COLLECTION", CollectionType: obj.Name,
			Identifier:     parentName + "." + field,
			Name:           ArtifactTypeName(members.Get.ReturnType.Self().AsObject.Value.Self().OriginalName),
			QualifiedName:  ArtifactTypeName(parentOriginal) + "-" + ArtifactTypeName(field),
			ItemType:       members.Get.ReturnType.Self().AsObject.Value.Self().OriginalName,
			KeyName:        members.Get.Args[0].Self().Name,
			KeyDescription: members.Get.Args[0].Self().Description,
		}
		item := &ModTreeNode{Parent: node, Name: "get", Type: members.Get.ReturnType, Description: members.Get.Description,
			Module: node.Module, OriginalModule: node.OriginalModule, DagqlServer: node.DagqlServer, CollectionDimension: dim, types: node.types}
		if err := walkArtifactNodes(ctx, item, visit, visiting); err != nil {
			return err
		}
		batch, err := CollectionBatchType(ctx, node.DagqlServer, node.Type.Self().AsObject.Value)
		if err != nil {
			return err
		}
		if batch.Valid {
			batchNode := &ModTreeNode{Parent: node, Name: "batch", Type: batch.Value,
				Module: node.Module, OriginalModule: node.OriginalModule, DagqlServer: node.DagqlServer, types: node.types}
			visit(batchNode)
			if err := walkArtifactBatch(ctx, batchNode, item, visit, visiting); err != nil {
				return err
			}
		}
		return nil
	}
	children, err := node.Children(ctx)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, child := range children {
		if seen[child.Name] {
			continue
		}
		seen[child.Name] = true
		if err := walkArtifactNodes(ctx, child, visit, visiting); err != nil {
			return err
		}
	}
	return nil
}

func walkArtifactBatch(ctx context.Context, batchNode, item *ModTreeNode, visit func(*ModTreeNode), visiting map[string]bool) error {
	children, err := batchNode.Children(ctx)
	if err != nil {
		return err
	}
	// Batch results are leaves. Enumerating collections below one
	// would execute a batch during discovery.
	seen := map[string]bool{}
	for _, child := range children {
		if typ := child.Type.Self(); !seen[child.Name] && typ != nil && !typ.Optional && typ.Kind == TypeDefKindObject {
			seen[child.Name] = true
			if artifactBatchDirective(child) != "" {
				// Use the item operation's address when it has a matching
				// batch. Batch-only operations use the same implicit dimension.
				counterpart, err := item.Child(ctx, child.Name)
				if err != nil {
					return err
				}
				if counterpart != nil && artifactBatchDirective(counterpart) == artifactBatchDirective(child) && counterpart.Type.Self().ToType().Name() == child.Type.Self().ToType().Name() {
					continue
				}
				receiver := *batchNode
				receiver.CollectionDimension = item.CollectionDimension
				child.Parent = &receiver
				// Checks and Generators have no runtime collections to
				// enumerate. Include Generator.stale without calling the batch.
				if err := walkArtifactNodes(ctx, child, visit, visiting); err != nil {
					return err
				}
			} else {
				visit(child)
			}
		}
	}
	return nil
}

// Projection changes workspace metadata, not the module function's contract.
// Evaluating the source node still calls the original Changeset or LLM field.
func projectArtifactNode(node *ModTreeNode) *ModTreeNode {
	if node == nil || node.ObjectType() == nil {
		return node
	}
	target := artifactProjectedTypeName(node)
	if target == node.ObjectType().Name {
		return node
	}
	if typ, ok := node.types[target]; ok {
		node = node.Clone()
		node.Type = typ
	}
	return node
}

// SchemaSelection applies presence and exact type keys before reading any items.
// Collection key values remain deferred until expansion.
func (a *Artifacts) SchemaSelection() (*Artifacts, error) {
	bound, err := a.BindDimensions()
	if err != nil {
		return nil, err
	}
	return bound.filter(func(item *Artifact) bool {
		if !bound.matchesDimensionFilters(item.DimensionDefinitions()) {
			return false
		}
		for _, filter := range bound.Selector.Dimensions {
			if strings.HasPrefix(filter.Dimension, "type:") && filter.Keys != nil && !slices.Contains(filter.Keys, strings.Join(item.Path, "/")) {
				return false
			}
		}
		return true
	}), nil
}

func (a *Artifact) setTypeDimension() {
	a.DimensionKeys = slices.DeleteFunc(slices.Clone(a.DimensionKeys), func(key *ArtifactDimensionKey) bool { return strings.HasPrefix(key.Dimension, "type:") })
	if a.TypeName != "" {
		a.DimensionKeys = append(a.DimensionKeys, &ArtifactDimensionKey{Dimension: "type:" + a.TypeName, Key: strings.Join(a.Path, "/")})
	}
}

func collectionDimensions(a *Artifact) []*ArtifactDimension {
	return slices.DeleteFunc(a.DimensionDefinitions(), func(d *ArtifactDimension) bool { return d.Kind == "TYPE" })
}

func artifactProjectedTypeName(node *ModTreeNode) string {
	target := node.ObjectType().Name
	switch node.ObjectType().Name {
	case "Changeset":
		if slices.Contains(node.Directives, "generate") {
			target = "Generator"
		}
	case "LLM":
		if slices.Contains(node.Directives, "agent") {
			target = "AgentMiddleware"
		}
	}
	return target
}
