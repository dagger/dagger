package core

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/dagger/dagger/core/artifact"
	"github.com/dagger/dagger/core/artifact/flattening"
	"github.com/dagger/dagger/core/dagaddress"
	"github.com/dagger/dagger/dagql"
	"golang.org/x/sync/errgroup"
)

type ArtifactDimension = artifact.Dimension

func (a *Artifact) dimensionName(identifier string) string {
	if name := a.DimensionNames[identifier]; name != "" {
		return name
	}
	if typeName, ok := strings.CutPrefix(identifier, "type:"); ok {
		return ArtifactTypeName(typeName)
	}
	return identifier
}

// displayNames maps each dimension to its display name. A name has one
// meaning in AllDimensions, so entries share these names.
func (a *Artifacts) displayNames() map[string]string {
	dimensions := artifact.Union(a.AllDimensions, artifact.Dimensions{{
		Kind: "NAME", Identifier: artifact.NameDimension, Name: artifact.NameDimension,
		QualifiedName: artifact.NameDimension, KeyName: "name",
	}})
	names := make(map[string]string, len(dimensions))
	for _, dim := range dimensions {
		names[dim.Identifier] = dimensions.DisplayName(dim)
	}
	return names
}

// dimensionNames selects the names of the artifact's own dimensions.
func dimensionNames(names map[string]string, a *Artifact) map[string]string {
	own := map[string]string{}
	for _, dim := range a.DimensionDefinitions() {
		own[dim.Identifier] = names[dim.Identifier]
	}
	return own
}

// NameEntries sets the display names and short type key that each entry's
// address uses.
func (a *Artifacts) NameEntries() {
	names := a.displayNames()
	for _, entry := range a.Entries {
		entry.DimensionNames = dimensionNames(names, entry)
		entry.TypeKeyName = a.typeKeyName(entry)
		entry.links = a.linkPlans(entry)
	}
}

func (a *Artifacts) linkPlans(entry *Artifact) *artifact.Links {
	if entry.Workspace.Self() == nil || entry.LoadFailure != nil || entry.TypeName == "" {
		return nil
	}
	path := artifact.Path{
		Type:       "type:" + entry.TypeName,
		Key:        strings.Join(entry.Path, "/"),
		Module:     entry.ModuleName,
		Dimensions: entry.projectedDimensions(),
	}
	cliName := func(name string) string {
		return ArtifactTypeName(strings.TrimPrefix(name, "type:"))
	}
	canonicalPath := path
	canonicalPath.Dimensions = nil
	for _, dimension := range entry.DimensionDefinitions() {
		if dimension.CollectionType != "" {
			canonicalPath.Dimensions = append(canonicalPath.Dimensions, dimension.CollectionType)
		}
	}
	return &artifact.Links{
		Typed:            artifact.PlanLink(a.AllPaths, path, a.CompleteSchema, true, cliName),
		Untyped:          artifact.PlanLink(a.AllPaths, path, a.CompleteSchema, false, cliName),
		PathNeedsAbsence: artifact.PathNeedsAbsentDimension(a.AllPaths, canonicalPath),
		CompleteSchema:   a.CompleteSchema,
	}
}

// typeKeyName is the shortest type key that names entry's path among the
// loaded paths with the collection dimensions its projected address keys, or
// "" for the full path. Without keys, an address keeps the full path: it then
// names a path across every module, and the full path is its stable name.
func (a *Artifacts) typeKeyName(entry *Artifact) string {
	dimensions := entry.projectedDimensions()
	if entry.TypeName == "" || len(dimensions) == 0 {
		return ""
	}
	path := strings.Join(entry.Path, "/")
	short := flattening.ShortKey(a.AllPaths, entry.TypeName, entry.Path, dimensions)
	if short == path {
		return ""
	}
	return short
}

// PathIndex lists the entries' schema paths for AllPaths.
func (a *Artifacts) PathIndex() artifact.Paths {
	var paths artifact.Paths
	for _, entry := range a.Entries {
		if entry.TypeName == "" {
			continue
		}
		path := artifact.Path{Type: "type:" + entry.TypeName, Key: strings.Join(entry.Path, "/"), Module: entry.ModuleName}
		for _, dim := range entry.DimensionDefinitions() {
			if !artifact.IsStaticDimension(dim.Identifier) {
				path.Dimensions = append(path.Dimensions, dim.Identifier)
				if dim.CollectionType != "" && dim.CollectionType != dim.Identifier {
					path.Dimensions = append(path.Dimensions, dim.CollectionType)
				}
			}
		}
		paths = append(paths, path)
	}
	return artifact.UnionPaths(paths)
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
	var collections flattening.Dimensions
	for node := a.Node; node != nil; node = node.Parent {
		if node.CollectionDimension != nil {
			collections = append(collections, (*flattening.Dimension)(node.CollectionDimension))
		}
	}
	slices.Reverse(collections)
	dims := flattening.ArtifactDimensions(a.ModuleName, a.TypeName, collections)
	result := make([]*ArtifactDimension, len(dims))
	for i, d := range dims {
		result[i] = (*ArtifactDimension)(d)
	}
	return result
}

func (a *Artifacts) DimensionDefinitions() artifact.Dimensions {
	sets := make([]artifact.Dimensions, 0, len(a.Entries))
	for _, entry := range a.Entries {
		sets = append(sets, entry.DimensionDefinitions())
	}
	return artifact.Union(sets...)
}

// dimensionMatches accepts canonical type IDs and explicit legacy route IDs.
func dimensionMatches(dimension *ArtifactDimension, identifier string) bool {
	return dimension != nil && (dimension.Identifier == identifier || dimension.CollectionType != "" && dimension.CollectionType == identifier)
}

// BindDimensions resolves names against AllDimensions. This is delayed until a
// result is requested, so filter order cannot bind an alias to a dimension
// that happened to have matching runtime keys.
func (a *Artifacts) BindDimensions() (*Artifacts, error) {
	bound := a.filter(func(*Artifact) bool { return true })
	bound.Selector.Dimensions = nil
	bound.Selector.DimensionAlternatives = nil
	selected := a.DimensionDefinitions()
	for _, filter := range a.Selector.Dimensions {
		id, err := a.ResolveDimension(filter.Dimension)
		if err != nil {
			return nil, err
		}
		bound.Selector.addDimension(ArtifactDimensionFilter{Dimension: id, Keys: filter.Keys})
	}
	for _, group := range a.Selector.DimensionAlternatives {
		ids := []string{}
		for _, name := range group {
			id, err := a.ResolveDimension(name)
			if err != nil {
				return nil, err
			}
			// Keep a name the selection has, or one a module that failed to
			// load could have: SchemaSelection decides for each failure, as
			// it does for a single name.
			present := id == artifact.NameDimension && slices.ContainsFunc(a.Entries, func(entry *Artifact) bool { return entry.TypeName != "" }) ||
				slices.ContainsFunc(selected, func(d *ArtifactDimension) bool { return dimensionMatches(d, id) }) ||
				!a.exact && slices.ContainsFunc(a.Entries, func(entry *Artifact) bool { return entry.mayHaveDimension(id, nil) })
			if present && !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
		if len(ids) == 1 {
			bound.Selector.addDimension(ArtifactDimensionFilter{Dimension: ids[0]})
		} else {
			bound.Selector.DimensionAlternatives = append(bound.Selector.DimensionAlternatives, ids)
		}
	}
	bound.resolveTypeKeys()
	return bound, nil
}

// resolveTypeKeys replaces each short type key filter with the full paths it
// names among the loaded paths that have every filtered collection dimension
// (see artifact.ResolveTypeKey). A full path names only itself, and a key
// that names nothing stays, matching nothing.
func (a *Artifacts) resolveTypeKeys() {
	if len(a.AllPaths) == 0 {
		return
	}
	paths := a.AllPaths
	var dimensions []string
	for _, filter := range a.Selector.Dimensions {
		switch {
		case filter.Dimension == artifact.ModuleDimension && filter.Keys != nil:
			paths = slices.DeleteFunc(slices.Clone(paths), func(path artifact.Path) bool { return !moduleKeysMatch(filter.Keys, path.Module) })
		case !artifact.IsStaticDimension(filter.Dimension):
			dimensions = append(dimensions, filter.Dimension)
		}
	}
	for i, filter := range a.Selector.Dimensions {
		if !strings.HasPrefix(filter.Dimension, "type:") || filter.Keys == nil {
			continue
		}
		candidates := paths.CandidatePaths(filter.Dimension, dimensions, nil)
		keys := make([]string, 0, len(filter.Keys))
		for _, key := range filter.Keys {
			full := []string{key}
			// A full path names itself, so a stored address stays exact.
			if !slices.ContainsFunc(candidates, func(path artifact.Path) bool { return path.Key == key }) {
				matches := artifact.ResolveTypeKey(candidates, key)
				if len(matches) > 0 {
					full = matches
				}
			}
			for _, key := range full {
				if !slices.Contains(keys, key) {
					keys = append(keys, key)
				}
			}
		}
		a.Selector.Dimensions[i].Keys = keys
	}
}

func (a *Artifacts) ResolveDimension(name string) (string, error) {
	if name == artifact.NameDimension {
		return artifact.NameDimension, nil
	}
	return a.AllDimensions.Resolve(name)
}

func (a *Artifacts) hasCollections() bool {
	return slices.ContainsFunc(a.DimensionDefinitions(), func(d *ArtifactDimension) bool { return !artifact.IsStaticDimension(d.Identifier) })
}

func (a *Artifacts) matchesDimensionFilters(dims []*ArtifactDimension) bool {
	for _, filter := range a.Selector.Dimensions {
		if filter.Keys != nil && len(filter.Keys) == 0 {
			return false
		}
		if !slices.ContainsFunc(dims, func(d *ArtifactDimension) bool { return dimensionMatches(d, filter.Dimension) }) {
			return false
		}
	}
	for _, group := range a.Selector.DimensionAlternatives {
		if !slices.ContainsFunc(dims, func(d *ArtifactDimension) bool {
			return slices.Contains(group, d.Identifier) || d.CollectionType != "" && slices.Contains(group, d.CollectionType)
		}) {
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
				!slices.ContainsFunc(parentDims, func(d *ArtifactDimension) bool { return dimensionMatches(d, dimension) }) {
				continue
			}
			if slices.EqualFunc(parentDims, childDims[:len(parentDims)], func(a, b *ArtifactDimension) bool { return a.Identifier == b.Identifier }) {
				return false
			}
		}
		return true
	})
}

// DimensionItems projects artifacts to the collection item that
// supplies dimension. Parent keys remain attached; descendant keys are removed.
func (a *Artifacts) DimensionItems(dimension string) ([]*Artifact, error) {
	if artifact.IsStaticDimension(dimension) {
		return nil, fmt.Errorf("dimensionItems requires a collection dimension, got %q", dimension)
	}
	items := []*Artifact{}
	seen := map[string]bool{}
	names := a.displayNames()
	for _, selected := range a.Entries {
		for node := selected.Node; node != nil; node = node.Parent {
			if node.CollectionDimension == nil || !dimensionMatches(node.CollectionDimension, dimension) {
				continue
			}
			members, err := node.Parent.ObjectType().CollectionMembers()
			if err != nil {
				return nil, err
			}
			itemNode := *node
			itemNode.Name = "get"
			itemNode.Type = members.Get.ReturnType
			itemNode.Description = members.Get.Description
			itemNode.Directives = nil
			item := *selected
			item.Node = &itemNode
			item.Path = itemNode.CommandCLIPath()
			if len(item.Path) == 0 {
				item.Path = itemNode.CLIPath()
			}
			item.TypeName = itemNode.ObjectType().Name
			item.Directives = nil
			item.addressPlans = nil
			item.DimensionKeys = nil
			for ancestor := &itemNode; ancestor != nil; ancestor = ancestor.Parent {
				if ancestor.CollectionDimension != nil && ancestor.CollectionKey != nil {
					item.DimensionKeys = append(item.DimensionKeys, &ArtifactDimensionKey{Dimension: ancestor.CollectionDimension.Identifier, Key: *ancestor.CollectionKey})
				}
			}
			slices.Reverse(item.DimensionKeys)
			if len(item.DimensionKeys) == 0 {
				// Compatibility for snapshots that carry keys without item nodes.
				for _, key := range selected.DimensionKeys {
					item.DimensionKeys = append(item.DimensionKeys, key)
					if key.Dimension == dimension || key.Dimension == node.CollectionDimension.Identifier {
						break
					}
				}
			}
			item.setStaticDimensions()
			item.DimensionNames = dimensionNames(names, &item)
			item.TypeKeyName = a.typeKeyName(&item)
			id, err := item.identity()
			if err != nil {
				return nil, err
			}
			if !seen[id] {
				item.links = a.linkPlans(&item)
				items = append(items, item.Clone())
				seen[id] = true
			}
			break
		}
	}
	return items, nil
}

// ExpandDimensionItems lists the collection items represented in the selection
// for a dimension (by identifier or name), evaluating only the collection
// receivers needed to enumerate their keys. Parent keys are preserved.
func (a *Artifacts) ExpandDimensionItems(ctx context.Context, name string) ([]*Artifact, error) {
	dimension, err := a.ResolveDimension(name)
	if err != nil {
		return nil, err
	}
	filtered, err := a.FilterDimensions([]string{name}).BindDimensions()
	if err != nil {
		return nil, err
	}
	expanded, err := filtered.ForDimensionKeys(dimension).Expand(ctx)
	if err != nil {
		return nil, err
	}
	return expanded.DimensionItems(dimension)
}

// ExpandDimensionKeys lists the keys represented in the selection for a
// dimension (by identifier or name), sorted with no duplicates. A static
// dimension's keys are read from the schema; a collection's are enumerated
// from its receivers, never from leaf values.
func (a *Artifacts) ExpandDimensionKeys(ctx context.Context, name string) ([]string, error) {
	dimension, err := a.ResolveDimension(name)
	if err != nil {
		return nil, err
	}
	if artifact.IsStaticDimension(dimension) {
		selected, err := a.FilterDimensions([]string{dimension}).SchemaSelection()
		if err != nil {
			return nil, err
		}
		var keys []string
		for _, entry := range selected.Entries {
			// A load failure the filter kept has no key of its own.
			if !entry.hasDimension(dimension) {
				continue
			}
			keys = append(keys, entry.StaticDimensionKey(dimension))
		}
		slices.Sort(keys)
		return slices.Compact(keys), nil
	}
	items, err := a.ExpandDimensionItems(ctx, name)
	if err != nil {
		return nil, err
	}
	return (&Artifacts{Entries: items}).DimensionKeys(dimension), nil
}

// Expand evaluates only the collection receivers needed to enumerate selected
// keys. A leaf artifact's value remains deferred.
func (a *Artifacts) Expand(ctx context.Context) (*Artifacts, error) {
	discovery, err := a.viewDiscovery()
	if err != nil {
		return nil, err
	}
	if len(discovery.view.state.Rules) == 0 {
		return a.expand(ctx, artifactCollectionKeys)
	}
	ctx = dagql.WithResultScope(ctx, nil)
	discoveryCtx := context.WithValue(ctx, artifactViewDiscoveryKey{}, discovery)
	roots := a.CollectionRoots
	if roots == nil {
		catalog := &Artifacts{}
		catalog.SetCollectionRoots(a.Entries)
		roots = catalog.CollectionRoots
	}
	var filters []ArtifactDimensionFilter
	for _, rule := range discovery.view.state.Rules {
		filters = append(filters, ArtifactDimensionFilter{Dimension: rule.Dimension, Keys: rule.Keys})
	}
	rootsCtx := withArtifactReceivers(discoveryCtx)
	for _, root := range roots {
		if !slices.ContainsFunc(filters, func(filter ArtifactDimensionFilter) bool {
			return dimensionMatches(root.Node.CollectionDimension, filter.Dimension)
		}) {
			continue
		}
		if _, err := expandArtifactNode(rootsCtx, root.Node, root, filters, artifactCollectionKeys); err != nil {
			return nil, err
		}
	}
	_, err = a.expand(discoveryCtx, artifactCollectionKeys)
	if err != nil {
		return nil, err
	}
	// Sort references by recipe so discovery order and local handles do not
	// change the view's cache identity.
	type sourceKey struct {
		index  int
		recipe string
		origin string
	}
	order := make([]sourceKey, len(discovery.view.sources))
	for i, source := range discovery.view.sources {
		id, err := source.RecipeID(ctx)
		if err != nil {
			return nil, err
		}
		encoded, err := id.Encode()
		if err != nil {
			return nil, err
		}
		order[i] = sourceKey{i, encoded, artifactViewOrigin(id)}
	}
	slices.SortFunc(order, func(a, b sourceKey) int { return strings.Compare(a.recipe, b.recipe) })
	view := &ArtifactView{state: artifactViewState{Rules: discovery.view.state.Rules}}
	if err := view.addPaths(ctx, roots); err != nil {
		return nil, err
	}
	for _, source := range order {
		view.sources = append(view.sources, discovery.view.sources[source.index])
		view.state.Keys = append(view.state.Keys, discovery.view.state.Keys[source.index])
		view.state.Origins = append(view.state.Origins, source.origin)
	}
	if err := view.setKey(); err != nil {
		return nil, err
	}
	selected := a.filter(func(*Artifact) bool { return true })
	for _, entry := range selected.Entries {
		entry.View = view
	}
	return selected.expand(ctx, func(ctx context.Context, receiver *Artifact) ([]collectionKey, error) {
		keys, err := artifactCollectionKeys(ctx, receiver)
		if err == nil {
			err = discovery.validate(receiver, keys)
		}
		return keys, err
	})
}

// artifactCollectionKeyFunc evaluates a collection receiver, never the item or leaf.
type artifactCollectionKeyFunc func(context.Context, *Artifact) ([]collectionKey, error)

func artifactCollectionKeys(ctx context.Context, artifact *Artifact) ([]collectionKey, error) {
	_, keys, err := artifactCollection(ctx, artifact)
	return keys, err
}

func artifactCollection(ctx context.Context, artifact *Artifact) (dagql.AnyObjectResult, []collectionKey, error) {
	ctx, err := artifact.WorkspaceContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	var value dagql.AnyObjectResult
	if err := artifact.Evaluate(ctx, &value); err != nil {
		return nil, nil, err
	}
	obj, ok := dagql.UnwrapAs[*ModuleObject](value)
	if !ok {
		return nil, nil, fmt.Errorf("returned %T instead of a collection", value.Unwrap())
	}
	artifactReceiversFrom(ctx).store(artifact, value)
	keys, err := obj.collectionKeys(ctx)
	return value, keys, err
}

// artifactReceivers holds the collection receivers one expansion has already
// evaluated, so a deeper receiver or item evaluates from its nearest one
// instead of selecting every ancestor again from the root. One expansion
// evaluates all its receivers in the same context, and a receiver's value
// also depends on the workspace and view it evaluates in, so values are kept
// per node and per those.
type artifactReceivers struct {
	mu     sync.Mutex
	values map[artifactReceiverKey]dagql.AnyObjectResult
}

type artifactReceiverKey struct {
	node             *ModTreeNode
	workspace        *Workspace
	contextWorkspace *Workspace
	view             *ArtifactView
}

type artifactReceiversKey struct{}

func withArtifactReceivers(ctx context.Context) context.Context {
	return context.WithValue(ctx, artifactReceiversKey{}, &artifactReceivers{values: map[artifactReceiverKey]dagql.AnyObjectResult{}})
}

func artifactReceiversFrom(ctx context.Context) *artifactReceivers {
	receivers, _ := ctx.Value(artifactReceiversKey{}).(*artifactReceivers)
	return receivers
}

func (r *artifactReceivers) key(a *Artifact, node *ModTreeNode) artifactReceiverKey {
	return artifactReceiverKey{node: node, workspace: a.Workspace.Self(), contextWorkspace: a.ContextWorkspace.Self(), view: a.View}
}

func (r *artifactReceivers) store(a *Artifact, value dagql.AnyObjectResult) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.values[r.key(a, a.Node)] = value
}

func (r *artifactReceivers) load(a *Artifact, node *ModTreeNode) (dagql.AnyObjectResult, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	value, ok := r.values[r.key(a, node)]
	return value, ok
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
			excluded, err := (&Artifacts{Entries: result.Entries, AllDimensions: a.AllDimensions, AllPaths: a.AllPaths}).filterURI(address, true)
			if err != nil {
				return nil, err
			}
			excluded, err = excluded.expand(ctx, collectionKeys)
			if err != nil {
				return nil, err
			}
			identities := map[string]bool{}
			for _, artifact := range excluded.Entries {
				id, err := artifact.identity()
				if err != nil {
					return nil, err
				}
				identities[id] = true
			}
			var identityErr error
			result = result.filter(func(artifact *Artifact) bool {
				id, err := artifact.identity()
				if err != nil {
					identityErr = err
				}
				return !identities[id]
			})
			if identityErr != nil {
				return nil, identityErr
			}
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
			item.setStaticDimensions()
		}
		return bound, nil
	}
	result := &Artifacts{Entries: []*Artifact{}, Selector: bound.Selector, AllDimensions: bound.AllDimensions, AllPaths: bound.AllPaths, CompleteSchema: bound.CompleteSchema, CollectionRoots: bound.CollectionRoots}
	names := bound.displayNames()
	ctx = withArtifactReceivers(ctx)
	// Each worker owns one slot; flatten only after all workers finish so
	// discovery order is independent of evaluation completion order.
	entries := make([][]*Artifact, len(bound.Entries))
	group, ctx := errgroup.WithContext(ctx)
	for i, template := range bound.Entries {
		// An exact collection path without its dimension selects the collection.
		if template.Node != nil && template.Node.CollectionDimension != nil && bound.hasExactPath(template.Path) && !bound.selectsDimension(template.Node.CollectionDimension.Identifier) && !bound.selectsDimension(template.Node.CollectionDimension.CollectionType) {
			continue
		}
		group.Go(func() error {
			nodes, err := expandArtifactNode(ctx, template.Node, template, bound.Selector.Dimensions, collectionKeys)
			if err != nil {
				return err
			}
			templateNames := dimensionNames(names, template)
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
				item.setStaticDimensions()
				item.DimensionNames = templateNames
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

// expandArtifactNode enumerates the keys of node's collections. Each
// collection receiver evaluates like template: in its workspace or context
// workspace.
func expandArtifactNode(ctx context.Context, node *ModTreeNode, template *Artifact, filters []ArtifactDimensionFilter, collectionKeys artifactCollectionKeyFunc) ([]*ModTreeNode, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if node == nil {
		return []*ModTreeNode{nil}, nil
	}
	parents, err := expandArtifactNode(ctx, node.Parent, template, filters, collectionKeys)
	if err != nil {
		return nil, err
	}
	var expanded []*ModTreeNode
	if node.CollectionDimension == nil || node.CollectionKey != nil {
		if node.CollectionDimension != nil && slices.ContainsFunc(filters, func(filter ArtifactDimensionFilter) bool {
			return dimensionMatches(node.CollectionDimension, filter.Dimension) && filter.Keys != nil && !slices.Contains(filter.Keys, *node.CollectionKey)
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
			receiver := &Artifact{Node: parent, Workspace: template.Workspace, ContextWorkspace: template.ContextWorkspace, View: template.View}
			var keys []collectionKey
			var err error
			if discovery, ok := ctx.Value(artifactViewDiscoveryKey{}).(*artifactViewDiscovery); ok {
				var value dagql.AnyObjectResult
				value, keys, err = artifactCollection(ctx, receiver)
				if err == nil {
					err = discovery.record(receiver, value, node.CollectionDimension, keys)
				}
			} else {
				keys, err = collectionKeys(ctx, receiver)
			}
			if err != nil {
				return fmt.Errorf("dimension %q: %w", node.CollectionDimension.Identifier, err)
			}
			for _, key := range keys {
				keep := true
				for _, filter := range filters {
					if dimensionMatches(node.CollectionDimension, filter.Dimension) && filter.Keys != nil && !slices.Contains(filter.Keys, key.text) {
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
		// Artifact dimensions use current CLI spelling, even when their
		// backing collection's GraphQL names belong to an older view.
		namer := LatestNamer
		path := []string{}
		parent := node
		for parent.Parent != nil && parent.CollectionDimension == nil {
			path = append(path, namer.CLIName(parent.Name))
			parent = parent.Parent
		}
		slices.Reverse(path)
		var parentDimension *flattening.Dimension
		if parent.CollectionDimension != nil {
			parentDimension = (*flattening.Dimension)(parent.CollectionDimension)
		}
		collection := flattening.Collection{
			Fields: path, Type: obj.Name,
			ItemType:       members.Get.ReturnType.Self().AsObject.Value.Self().Name,
			KeyName:        members.Get.Args[0].Self().Name,
			KeyDescription: members.Get.Args[0].Self().Description,
		}
		dimension := collection.Dimension(node.Module.Self().Name(), parentDimension, nil)
		if Supports(ctx, ArtifactTypeColumnsVersion) {
			dimension = collection.CanonicalDimension()
			for ancestor := node; ancestor != nil; ancestor = ancestor.Parent {
				if ancestor.CollectionDimension != nil && ancestor.CollectionDimension.CollectionType == collection.Type {
					return fmt.Errorf("collection type %s repeats on one artifact route", collection.Type)
				}
			}
		}
		dim := (*ArtifactDimension)(dimension)
		item := &ModTreeNode{Parent: node, Name: "get", Type: members.Get.ReturnType, Description: members.Get.Description,
			Module: node.Module, OriginalModule: node.OriginalModule, DagqlServer: node.DagqlServer, CollectionDimension: dim, types: node.types}
		if err := walkArtifactNodes(ctx, item, visit, visiting); err != nil {
			return err
		}
		for _, fn := range obj.Functions {
			if fn.Self().Name == members.Get.Name {
				continue
			}
			child, err := node.Child(ctx, fn.Self().Name)
			if err != nil {
				return err
			}
			if child != nil {
				if err := walkArtifactNodes(ctx, child, visit, visiting); err != nil {
					return err
				}
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

// SchemaSelection applies module, type, and presence filters without loading collection keys.
func (a *Artifacts) SchemaSelection() (*Artifacts, error) {
	bound, err := a.BindDimensions()
	if err != nil {
		return nil, err
	}
	return bound.filter(func(item *Artifact) bool {
		has := func(id string) bool {
			return item.hasDimension(id)
		}
		for _, filter := range bound.Selector.Dimensions {
			if filter.Keys != nil && len(filter.Keys) == 0 {
				return false
			}
			matched := has(filter.Dimension) && (!artifact.IsStaticDimension(filter.Dimension) || filter.Keys == nil ||
				filter.Dimension == artifact.ModuleDimension && moduleKeysMatch(filter.Keys, item.ModuleName) ||
				slices.Contains(filter.Keys, item.StaticDimensionKey(filter.Dimension)))
			if !matched && (bound.exact || !item.mayHaveDimension(filter.Dimension, filter.Keys)) {
				return false
			}
		}
		for _, group := range bound.Selector.DimensionAlternatives {
			if !slices.ContainsFunc(group, func(id string) bool {
				return has(id) || !bound.exact && item.mayHaveDimension(id, nil)
			}) {
				return false
			}
		}
		return true
	}), nil
}

func (a *Artifact) setStaticDimensions() {
	keys := make([]flattening.Coordinate, 0, len(a.DimensionKeys))
	for _, key := range a.DimensionKeys {
		keys = append(keys, flattening.Coordinate{Dimension: key.Dimension, Key: key.Key})
	}
	coordinates := flattening.Coordinates(a.ModuleName, a.Path, a.TypeName, keys)
	a.DimensionKeys = make([]*ArtifactDimensionKey, 0, len(coordinates))
	for _, key := range coordinates {
		a.DimensionKeys = append(a.DimensionKeys, &ArtifactDimensionKey{Dimension: key.Dimension, Key: key.Key})
	}
}

func collectionDimensions(a *Artifact) []*ArtifactDimension {
	return slices.DeleteFunc(a.DimensionDefinitions(), func(d *ArtifactDimension) bool { return artifact.IsStaticDimension(d.Identifier) })
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
			target = "Expertise"
		}
	}
	return target
}

func (a *Artifact) StaticDimensionKey(dimension string) string {
	if dimension == artifact.ModuleDimension {
		return a.ModuleName
	}
	if dimension == artifact.NameDimension {
		coordinates := a.CanonicalCoordinates()
		return coordinates[len(coordinates)-1].Key
	}
	return strings.Join(a.Path, "/")
}
