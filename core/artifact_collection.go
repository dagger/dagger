package core

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strings"

	"github.com/dagger/dagger/core/artifact"
)

type ArtifactDimension = artifact.Dimension

func (a *Artifact) DimensionDefinitions() []*ArtifactDimension {
	var dims []*ArtifactDimension
	if a.ModuleName != "" {
		dims = append([]*ArtifactDimension{{Kind: "MODULE", Identifier: artifact.ModuleDimension, Name: "module", QualifiedName: "module"}}, dims...)
	}
	if a.TypeName != "" {
		dims = append(dims, &ArtifactDimension{Kind: "TYPE", Identifier: "type:" + a.TypeName, Name: ArtifactTypeName(a.TypeName), QualifiedName: "artifact-" + ArtifactTypeName(a.TypeName), ItemType: a.TypeName})
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

// Expand sets the module and type keys of the selected artifacts.
func (a *Artifacts) Expand(ctx context.Context) (*Artifacts, error) {
	bound, err := a.SchemaSelection()
	if err != nil {
		return nil, err
	}
	for _, item := range bound.Entries {
		item.setStaticDimensions()
	}
	return bound, nil
}

func artifactIdentity(artifact *Artifact) string {
	// Identity uses exact dimension identifiers, independent of display aliases.
	encoded, _ := json.Marshal(struct {
		Path []string
		Keys []*ArtifactDimensionKey
	}{artifact.Path, artifact.DimensionKeys})
	return string(encoded)
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
	if node.Parent != nil && node.RootValue == nil {
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
		if !bound.matchesDimensionFilters(item.DimensionDefinitions()) {
			return false
		}
		for _, filter := range bound.Selector.Dimensions {
			if artifact.IsStaticDimension(filter.Dimension) && filter.Keys != nil && !slices.Contains(filter.Keys, item.StaticDimensionKey(filter.Dimension)) {
				return false
			}
		}
		return true
	}), nil
}

func (a *Artifact) setStaticDimensions() {
	a.DimensionKeys = slices.DeleteFunc(slices.Clone(a.DimensionKeys), func(key *ArtifactDimensionKey) bool { return artifact.IsStaticDimension(key.Dimension) })
	if a.ModuleName != "" {
		a.DimensionKeys = append([]*ArtifactDimensionKey{{Dimension: artifact.ModuleDimension, Key: a.ModuleName}}, a.DimensionKeys...)
	}
	if a.TypeName != "" {
		a.DimensionKeys = append(a.DimensionKeys, &ArtifactDimensionKey{Dimension: "type:" + a.TypeName, Key: strings.Join(a.Path, "/")})
	}
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
	return strings.Join(a.Path, "/")
}
