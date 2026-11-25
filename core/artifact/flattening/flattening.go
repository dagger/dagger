package flattening

import (
	"strings"

	"github.com/dagger/dagger/engine/naming"
)

type Collection struct {
	Fields         []string
	Type           string
	ItemType       string
	KeyName        string
	KeyDescription string
}

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

func (c Collection) CanonicalDimension() *Dimension {
	return &Dimension{
		Kind: "COLLECTION", Identifier: c.Type, CollectionType: c.Type,
		Name: latestName(c.ItemType), QualifiedName: c.Type,
		ItemType: c.ItemType, KeyName: c.KeyName, KeyDescription: c.KeyDescription,
	}
}

type Coordinate struct{ Dimension, Key string }

func latestName(name string) string {
	return naming.MapNamePattern(name, func(run string) string {
		id, err := naming.Latest.Parse(run)
		if err != nil {
			return run
		}
		return id.Format(naming.Kebab, naming.Uppercase)
	})
}
