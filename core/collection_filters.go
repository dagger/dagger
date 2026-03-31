package core

import (
	"encoding/json"

	"github.com/vektah/gqlparser/v2/ast"
)

type CollectionFilterInput struct {
	CollectionType string   `name:"typeName"`
	Values         []string `name:"values"`
}

func (CollectionFilterInput) TypeName() string {
	return "CollectionFilterInput"
}

type CollectionFilterValues struct {
	TypeName string   `field:"true" doc:"The collection type name."`
	Values   []string `field:"true" doc:"The raw filter values available for this collection type."`
}

func (*CollectionFilterValues) Type() *ast.Type {
	return &ast.Type{
		NamedType: "CollectionFilterValues",
		NonNull:   true,
	}
}

func (*CollectionFilterValues) TypeDescription() string {
	return "Values available for a collection-aware CLI filter."
}

type CollectionFilterSet struct {
	byType map[string][]string
}

func NewCollectionFilterSet(filters []CollectionFilterInput) CollectionFilterSet {
	set := CollectionFilterSet{}
	if len(filters) == 0 {
		return set
	}

	set.byType = make(map[string][]string, len(filters))
	for _, filter := range filters {
		key := gqlObjectName(filter.CollectionType)
		set.byType[key] = append(set.byType[key], filter.Values...)
	}
	return set
}

func (set CollectionFilterSet) Clone() CollectionFilterSet {
	if len(set.byType) == 0 {
		return CollectionFilterSet{}
	}

	cp := CollectionFilterSet{
		byType: make(map[string][]string, len(set.byType)),
	}
	for typeName, values := range set.byType {
		cp.byType[typeName] = append([]string(nil), values...)
	}
	return cp
}

func (set CollectionFilterSet) HasAny() bool {
	return len(set.byType) > 0
}

func (set CollectionFilterSet) ValuesFor(typeName string) ([]string, bool) {
	if len(set.byType) == 0 {
		return nil, false
	}
	values, ok := set.byType[gqlObjectName(typeName)]
	if !ok {
		return nil, false
	}
	return append([]string(nil), values...), true
}

func normalizeCollectionFilterValue(keyType *TypeDef, value any) (string, error) {
	input, err := keyType.ToInput().Decoder().DecodeInput(value)
	if err != nil {
		return "", err
	}
	return collectionFilterValueString(input.ToLiteral().ToInput())
}

func collectionFilterValueString(value any) (string, error) {
	if str, ok := value.(string); ok {
		return str, nil
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(payload), nil
}
