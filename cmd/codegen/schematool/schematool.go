package schematool

import "github.com/dagger/dagger/cmd/codegen/introspection"

// Merge appends the types declared in mod to schema. If a type name
// in mod collides with an existing type in schema, Merge returns an
// error.
//
// Merge preserves directive metadata by stamping each inserted type
// with a @sourceModuleName directive carrying mod.Name.
func Merge(schema *introspection.Schema, mod *ModuleTypes) error {
	return mergeInto(schema, mod)
}

// ListTypes returns the names of types in schema matching the given
// kind filter. An empty kind matches all types.
func ListTypes(schema *introspection.Schema, kind string) []string {
	return listTypes(schema, kind)
}

// HasType reports whether schema contains a type with the given name.
func HasType(schema *introspection.Schema, name string) bool {
	return schema.Types.Get(name) != nil
}

// DescribeType returns the schema.Type with the given name, or nil.
func DescribeType(schema *introspection.Schema, name string) *introspection.Type {
	return schema.Types.Get(name)
}
