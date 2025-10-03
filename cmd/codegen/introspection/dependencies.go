package introspection

// DependencyModule represents a module that is a dependency in the schema.
// Dependencies are identified by having a SourceMap directive with a non-empty Filename,
// which indicates they come from a user module rather than the core Dagger API.
type DependencyModule struct {
	// Name is the field name in the Query type (e.g., "myDependency")
	Name string

	// TypeName is the GraphQL type name that the field returns (e.g., "MyDependency")
	TypeName string

	// Functions are all the fields/methods available on this dependency module
	Functions []*Field

	// SourceMap contains information about where this module is defined
	SourceMap *SourceMap
}

// ExtractDependencies returns all modules that are dependencies in the schema.
// A dependency is identified as a field on the Query type that:
//  1. Returns an object type
//  2. Has a SourceMap directive with a non-empty Filename
//
// This distinguishes user module dependencies from core Dagger API types.
func (s *Schema) ExtractDependencies() []*DependencyModule {
	if s == nil {
		return nil
	}

	queryType := s.Query()
	if queryType == nil {
		return nil
	}

	var deps []*DependencyModule

	for _, field := range queryType.Fields {
		if field.IsDependencyField() {
			// Get the type name from the field's return type
			typeName := field.TypeRef.UnwrappedTypeName()
			if typeName == "" {
				continue
			}

			// Get the actual type definition
			depType := s.Types.Get(typeName)
			if depType == nil {
				continue
			}

			deps = append(deps, &DependencyModule{
				Name:      field.Name,
				TypeName:  typeName,
				Functions: depType.Fields,
				SourceMap: field.Directives.SourceMap(),
			})
		}
	}

	return deps
}

// IsDependencyField checks if a field represents a dependency module.
// A field is a dependency if it has a SourceMap directive with a non-empty Filename.
func (f *Field) IsDependencyField() bool {
	if f.TypeRef == nil || !f.TypeRef.IsObject() {
		return false
	}

	sourceMap := f.Directives.SourceMap()
	return sourceMap != nil && sourceMap.Filename != ""
}

// UnwrappedTypeName extracts the underlying type name from a TypeRef,
// unwrapping through NON_NULL and LIST wrappers.
// For example:
//   - [String!]! -> "String"
//   - MyType! -> "MyType"
//   - [Container] -> "Container"
func (r *TypeRef) UnwrappedTypeName() string {
	if r == nil {
		return ""
	}

	switch r.Kind {
	case TypeKindObject, TypeKindScalar, TypeKindEnum, TypeKindInterface, TypeKindUnion, TypeKindInputObject:
		return r.Name
	case TypeKindNonNull, TypeKindList:
		if r.OfType != nil {
			return r.OfType.UnwrappedTypeName()
		}
		return ""
	default:
		return ""
	}
}

// UnwrappedType returns the underlying Type after unwrapping NON_NULL and LIST wrappers.
// This returns the TypeRef itself (not just the name) so you can check its Kind, etc.
func (r *TypeRef) UnwrappedType() *TypeRef {
	if r == nil {
		return nil
	}

	switch r.Kind {
	case TypeKindNonNull, TypeKindList:
		if r.OfType != nil {
			return r.OfType.UnwrappedType()
		}
		return nil
	default:
		return r
	}
}
