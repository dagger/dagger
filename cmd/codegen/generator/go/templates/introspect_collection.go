package templates

import (
	"fmt"

	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/iancoleman/strcase"
)

// Self-call bindings must use the same collection schema as engine clients.
func introspectCollection(spec *parsedObjectType) (*introspection.Type, *introspection.Type, error) {
	var keys *fieldSpec
	var get *funcTypeSpec
	for _, field := range spec.fields {
		if !field.isPrivate && strcase.ToLowerCamel(field.name) == "keys" {
			keys = field
		}
	}
	for _, field := range spec.fields {
		if field.isCollectionKeys {
			keys = field
		}
	}
	for _, method := range spec.methods {
		if strcase.ToLowerCamel(method.name) == "get" {
			get = method
		}
	}
	for _, method := range spec.methods {
		if method.isCollectionGet {
			get = method
		}
	}
	if keys == nil || get == nil {
		return nil, nil, fmt.Errorf("collection %q requires a stored keys field and a get function", spec.name)
	}
	lookup := introspectMethod(get)
	if len(lookup.Args) != 1 {
		return nil, nil, fmt.Errorf("collection %q get function requires one argument", spec.name)
	}
	lookup.Name = "get"
	lookup.Args[0].Name = "key"
	name := introspectTypeName(spec.name, spec.moduleName)
	self := &introspection.TypeRef{Kind: introspection.TypeKindNonNull, OfType: &introspection.TypeRef{Kind: introspection.TypeKindObject, Name: name}}
	keyList := introspectTypeRef(keys.typeSpec)
	collection := &introspection.Type{Kind: introspection.TypeKindObject, Name: name, Description: spec.doc, Interfaces: []*introspection.Type{},
		Fields: []*introspection.Field{
			{Name: "keys", TypeRef: keyList, Args: introspection.InputValues{}},
			{Name: "list", TypeRef: &introspection.TypeRef{Kind: introspection.TypeKindNonNull, OfType: &introspection.TypeRef{Kind: introspection.TypeKindList, OfType: lookup.TypeRef}}, Args: introspection.InputValues{}},
			lookup,
			{Name: "subset", TypeRef: self, Args: introspection.InputValues{{Name: "keys", TypeRef: keyList}}},
			introspectNodeIDField(name),
		}}
	var batch *introspection.Type
	for _, method := range spec.methods {
		if method == get {
			continue
		}
		if batch == nil {
			batch = &introspection.Type{Kind: introspection.TypeKindObject, Name: name + "_Batch", Interfaces: []*introspection.Type{}}
		}
		batch.Fields = append(batch.Fields, introspectMethod(method))
	}
	if batch != nil {
		batch.Fields = append(batch.Fields, introspectNodeIDField(batch.Name))
		collection.Fields = append(collection.Fields, &introspection.Field{Name: "batch",
			TypeRef: &introspection.TypeRef{Kind: introspection.TypeKindNonNull, OfType: &introspection.TypeRef{Kind: introspection.TypeKindObject, Name: batch.Name}},
			Args:    introspection.InputValues{}})
	}
	return collection, batch, nil
}
