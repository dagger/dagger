package generator

import (
	"github.com/dagger/dagger/codegen/introspection"
)

const (
	QueryStructName       = "Query"
	QueryStructClientName = "Client"
)

// CustomScalar registers custom Dagger type.
// TODO: This may done it dynamically later instead of a static
// map.
var CustomScalar = map[string]string{
	"ContainerID":      "Container",
	"FileID":           "File",
	"DirectoryID":      "Directory",
	"SecretID":         "Secret",
	"SocketID":         "Socket",
	"CacheID":          "CacheVolume",
	"ProjectID":        "Project",
	"ProjectCommandID": "ProjectCommand",
}

// FormatTypeFuncs is an interface to format any GraphQL type.
// Each generator has to implement this interface.
type FormatTypeFuncs interface {
	FormatKindList(representation string) string
	FormatKindScalarString(representation string) string
	FormatKindScalarInt(representation string) string
	FormatKindScalarFloat(representation string) string
	FormatKindScalarBoolean(representation string) string
	FormatKindScalarDefault(representation string, refName string, input bool) string
	FormatKindObject(representation string, refName string) string
	FormatKindInputObject(representation string, refName string) string
	FormatKindEnum(representation string, refName string) string
}

// CommonFunctions formatting function with global shared template functions.
type CommonFunctions struct {
	formatTypeFuncs FormatTypeFuncs
}

func NewCommonFunctions(formatTypeFuncs FormatTypeFuncs) *CommonFunctions {
	return &CommonFunctions{formatTypeFuncs: formatTypeFuncs}
}

// FormatReturnType formats a GraphQL type into the SDK language output,
// unless it's an ID that will be converted which needs to be formatted
// as an input (for chaining).
func (c *CommonFunctions) FormatReturnType(f introspection.Field) string {
	return c.formatType(f.TypeRef, c.ConvertID(f))
}

// ConvertID returns true if the field returns an ID that should be
// converted into an object.
func (c *CommonFunctions) ConvertID(f introspection.Field) bool {
	if f.Name == "id" {
		return false
	}
	ref := f.TypeRef
	if ref.Kind == introspection.TypeKindNonNull {
		ref = ref.OfType
	}
	if ref.Kind != introspection.TypeKindScalar {
		return false
	}

	// FIXME: As of now all custom scalars are IDs. If that changes we
	// need to make sure we can tell the difference.
	alias, ok := CustomScalar[ref.Name]

	// FIXME: We don't have a simple way to convert any ID to its
	// corresponding object (in codegen) so for now just return the
	// current instance. Currently, `sync` is the only field where
	// the error is what we care about but more could be added later.
	// To avoid wasting a result, we return the ID which is a leaf value
	// and triggers execution, but then convert to object in the SDK to
	// allow continued chaining. For this, we're assuming the returned
	// ID represents the exact same object but if that changes, we'll
	// need to adjust.
	return ok && alias == f.ParentObject.Name
}

// FormatInputType formats a GraphQL type into the SDK language input
//
// Example: `String` -> `string`
func (c *CommonFunctions) FormatInputType(r *introspection.TypeRef) string {
	return c.formatType(r, true)
}

// FormatOutputType formats a GraphQL type into the SDK language output
//
// Example: `String` -> `string`
func (c *CommonFunctions) FormatOutputType(r *introspection.TypeRef) string {
	return c.formatType(r, false)
}

// formatType loops through the type reference to transform it into its SDK language.
func (c *CommonFunctions) formatType(r *introspection.TypeRef, input bool) (representation string) {
	for ref := r; ref != nil; ref = ref.OfType {
		switch ref.Kind {
		case introspection.TypeKindList:
			// Handle this special case with defer to format array at the end of
			// the loop.
			// Since an SDK needs to insert it at the end, other at the beginning.
			defer func() {
				representation = c.formatTypeFuncs.FormatKindList(representation)
			}()
		case introspection.TypeKindScalar:
			switch introspection.Scalar(ref.Name) {
			case introspection.ScalarString:
				return c.formatTypeFuncs.FormatKindScalarString(representation)
			case introspection.ScalarInt:
				return c.formatTypeFuncs.FormatKindScalarInt(representation)
			case introspection.ScalarFloat:
				return c.formatTypeFuncs.FormatKindScalarFloat(representation)
			case introspection.ScalarBoolean:
				return c.formatTypeFuncs.FormatKindScalarBoolean(representation)
			default:
				return c.formatTypeFuncs.FormatKindScalarDefault(representation, ref.Name, input)
			}
		case introspection.TypeKindObject:
			return c.formatTypeFuncs.FormatKindObject(representation, ref.Name)
		case introspection.TypeKindInputObject:
			return c.formatTypeFuncs.FormatKindInputObject(representation, ref.Name)
		case introspection.TypeKindEnum:
			return c.formatTypeFuncs.FormatKindEnum(representation, ref.Name)
		}
	}

	panic(r)
}
