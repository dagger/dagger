// Package flattening maps artifact schema paths and collection items to
// dimensions. It does not load modules, evaluate collections, or format URLs.
package flattening

import (
	"fmt"
	"slices"
	"strings"

	"github.com/dagger/dagger/engine/naming"
)

// Collection describes a collection boundary on an artifact's route. Fields
// are relative to the previous collection item, or to the module root for the
// first boundary. They name object fields, not synthetic get operations.
type Collection struct {
	Fields []string
	// Type is the collection object type; ItemType is its lookup result type.
	Type           string
	ItemType       string
	KeyName        string
	KeyDescription string
}

// Dimension retains the current path-based collection identity. Parent is the
// preceding collection boundary. Ordinary object fields extend its path.
// A nil name function uses current artifact spelling.
func (c Collection) Dimension(module string, parent *Dimension, name func(string) string) *Dimension {
	if name == nil {
		name = latestName
	}
	prefix := "/" + name(module)
	if parent != nil {
		prefix = parent.Identifier
	}
	fields := make([]string, len(c.Fields))
	for i, field := range c.Fields {
		fields[i] = name(field)
	}
	id := strings.Join(append([]string{prefix}, fields...), "/")
	itemName := name(c.ItemType)
	qualified := strings.Split(strings.TrimPrefix(id, "/"), "/")
	item := itemName
	for _, prefix := range []string{qualified[0], name(module)} {
		if rest, ok := strings.CutPrefix(item, prefix+"-"); ok {
			item = rest
			break
		}
	}
	return &Dimension{Kind: "COLLECTION", CollectionType: c.Type, Identifier: id,
		Name: itemName, QualifiedName: strings.Join(append(qualified, item), "-"), ItemType: c.ItemType,
		KeyName: c.KeyName, KeyDescription: c.KeyDescription}
}

// Artifact is a schema path, including paths whose collections are empty.
// Path is the public field path; a main module can omit its module prefix.
// Collections lists boundaries from the module root towards this artifact.
type Artifact struct {
	Module string
	Path   []string
	// Type is the concrete artifact type, such as Check or Container.
	Type        string
	Collections []Collection
}

func (a Artifact) dimensions() Dimensions {
	var collections Dimensions
	var parent *Dimension
	for _, c := range a.Collections {
		parent = c.Dimension(a.Module, parent, nil)
		collections = append(collections, parent)
	}
	return ArtifactDimensions(a.Module, a.Type, collections)
}

// ArtifactDimensions adds the current special module and type dimensions.
// Collection dimensions remain in parent-to-child order.
func ArtifactDimensions(module, typ string, collections Dimensions) Dimensions {
	dims := slices.Clone(collections)
	if module != "" {
		dims = append(Dimensions{&Dimension{Kind: "MODULE", Identifier: ModuleDimension, Name: "module", QualifiedName: "module", KeyName: "name"}}, dims...)
	}
	if typ != "" {
		name := latestName(typ)
		dims = append(dims, &Dimension{Kind: "TYPE", Identifier: "type:" + typ, Name: name, QualifiedName: "artifact-" + name, ItemType: typ, KeyName: "name"})
	}
	return dims
}

// Coordinate is one dimension's value for an artifact. Collection values are
// supplied by discovery; flattening only assigns their dimensions.
type Coordinate struct{ Dimension, Key string }

// Coordinates replaces static coordinates and retains collection coordinates.
// Type coordinates always hold the full field path, even when display can use
// an unambiguous suffix. Module names retain their configured spelling.
func Coordinates(module string, path []string, typ string, keys []Coordinate) []Coordinate {
	coordinates := make([]Coordinate, 0, len(keys)+2)
	if module != "" {
		coordinates = append(coordinates, Coordinate{ModuleDimension, module})
	}
	for _, key := range keys {
		if !IsStaticDimension(key.Dimension) {
			coordinates = append(coordinates, key)
		}
	}
	if typ != "" {
		coordinates = append(coordinates, Coordinate{"type:" + typ, strings.Join(path, "/")})
	}
	return coordinates
}

// Plan retains the complete schema. Construct it before selecting rows:
// neither an artifact filter nor an empty collection may change its names.
type Plan struct {
	dimensions Dimensions
	paths      Paths
}

// New builds the current flattening from all artifact paths in the workspace.
func New(schema []Artifact) *Plan {
	var sets []Dimensions
	var paths Paths
	for _, a := range schema {
		dims := a.dimensions()
		sets = append(sets, dims)
		if a.Type == "" {
			continue
		}
		path := Path{Type: "type:" + a.Type, Key: strings.Join(a.Path, "/"), Module: a.Module}
		for _, d := range dims {
			if !IsStaticDimension(d.Identifier) {
				path.Dimensions = append(path.Dimensions, d.Identifier)
			}
		}
		paths = append(paths, path)
	}
	return &Plan{dimensions: Union(sets...), paths: UnionPaths(paths)}
}

// Dimensions returns schema columns in identifier order. Callers own the copy.
func (p *Plan) Dimensions() Dimensions {
	result := make(Dimensions, len(p.dimensions))
	for i, d := range p.dimensions {
		copy := *d
		result[i] = &copy
	}
	return result
}

// Names returns the current shortest unambiguous dimension names.
func (p *Plan) Names() map[string]string {
	names := make(map[string]string, len(p.dimensions))
	for _, d := range p.dimensions {
		names[d.Identifier] = p.dimensions.DisplayName(d)
	}
	return names
}

// ShortKey keeps the full path without collection coordinates. With them, it
// finds the shortest unique suffix among all paths with those dimensions.
func ShortKey(paths Paths, typ string, path []string, dimensions []string) string {
	full := strings.Join(path, "/")
	if typ == "" || len(dimensions) == 0 {
		return full
	}
	return ShortTypeKey(paths.Candidates("type:"+typ, dimensions, nil), full)
}

// Row contains full identity coordinates and the separately shortened type key.
// A short type key is valid only alongside this row's collection dimensions.
type Row struct {
	Coordinates []Coordinate
	TypeKey     string
}

// Flatten supplies one key per collection boundary, in parent-to-child order.
// The artifact must have a type and be in the complete schema passed to New.
func (p *Plan) Flatten(a Artifact, keys ...string) (Row, error) {
	if a.Type == "" {
		return Row{}, fmt.Errorf("artifact %s has no type", strings.Join(a.Path, "/"))
	}
	if len(keys) != len(a.Collections) {
		return Row{}, fmt.Errorf("artifact %s needs %d collection keys, got %d", strings.Join(a.Path, "/"), len(a.Collections), len(keys))
	}
	var coordinates []Coordinate
	var dimensions []string
	for _, d := range a.dimensions() {
		if !IsStaticDimension(d.Identifier) {
			dimensions = append(dimensions, d.Identifier)
		}
	}
	full := strings.Join(a.Path, "/")
	if !slices.ContainsFunc(p.paths, func(path Path) bool {
		return path.Module == a.Module && path.Type == "type:"+a.Type && path.Key == full && slices.Equal(path.Dimensions, dimensions)
	}) {
		return Row{}, fmt.Errorf("artifact %s is absent from the flattening schema", full)
	}
	for i, d := range dimensions {
		coordinates = append(coordinates, Coordinate{d, keys[i]})
	}
	return Row{Coordinates: Coordinates(a.Module, a.Path, a.Type, coordinates), TypeKey: ShortKey(p.paths, a.Type, a.Path, dimensions)}, nil
}

// latestName follows the current artifact CLI spelling, including path syntax.
func latestName(name string) string {
	return naming.MapNamePattern(name, func(run string) string {
		id, err := naming.Latest.Parse(run)
		if err != nil {
			return run
		}
		return id.Format(naming.Kebab, naming.Uppercase)
	})
}
