// Package flattening maps artifact schema paths and collection items to
// dimensions. It does not load modules, evaluate collections, or format URLs.
package flattening

import (
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

// Coordinate is one dimension's value for an artifact. Collection values are
// supplied by discovery; flattening only assigns their dimensions.
type Coordinate struct{ Dimension, Key string }

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
