package generator

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/dagger/dagger/cmd/codegen/introspection"
)

const (
	QueryStructName       = "Query"
	QueryStructClientName = "Client"
)

// FormatTypeFuncs is an interface to format any GraphQL type.
// Each generator has to implement this interface.
type FormatTypeFuncs interface {
	FormatKindList(representation string) string
	FormatKindScalarString(representation string) string
	FormatKindScalarInt(representation string) string
	FormatKindScalarFloat(representation string) string
	FormatKindScalarBoolean(representation string) string
	FormatKindScalarDefault(representation string, refName string, input bool) string
	FormatKindObject(representation string, refName string, input bool) string
	FormatKindInputObject(representation string, refName string, input bool) string
	FormatKindEnum(representation string, refName string) string
}

// CommonFunctions formatting function with global shared template functions.
type CommonFunctions struct {
	formatTypeFuncs FormatTypeFuncs
}

func NewCommonFunctions(formatTypeFuncs FormatTypeFuncs) *CommonFunctions {
	return &CommonFunctions{formatTypeFuncs: formatTypeFuncs}
}

// IsSelfChainable returns true if an object type has any fields that return that same type.
func (c *CommonFunctions) IsSelfChainable(t introspection.Type) bool {
	for _, f := range t.Fields {
		// Only consider fields that return a non-null object.
		if !f.TypeRef.IsObject() || f.TypeRef.Kind != introspection.TypeKindNonNull {
			continue
		}
		if f.TypeRef.OfType.Name == t.Name {
			return true
		}
	}
	return false
}

func (c *CommonFunctions) InnerType(t *introspection.TypeRef) *introspection.TypeRef {
	switch t.Kind {
	case introspection.TypeKindNonNull:
		return c.InnerType(t.OfType)
	case introspection.TypeKindList:
		return c.InnerType(t.OfType)
	default:
		return t
	}
}

func (c *CommonFunctions) ObjectName(t *introspection.TypeRef) (string, error) {
	switch t.Kind {
	case introspection.TypeKindNonNull:
		return c.ObjectName(t.OfType)
	case introspection.TypeKindObject:
		return t.Name, nil
	default:
		return "", fmt.Errorf("unexpected type kind %s", t.Kind)
	}
}

func (c *CommonFunctions) IsIDableObject(t *introspection.TypeRef) (bool, error) {
	schema := GetSchema()
	switch t.Kind {
	case introspection.TypeKindNonNull:
		return c.IsIDableObject(t.OfType)
	case introspection.TypeKindObject:
		schemaType := schema.Types.Get(t.Name)
		if schemaType == nil {
			return false, fmt.Errorf("schema type %s is nil", t.Name)
		}

		for _, f := range schemaType.Fields {
			if f.Name == "id" {
				return true, nil
			}
		}
		return false, nil
	default:
		return false, nil
	}
}

// FormatReturnType formats a GraphQL type into the SDK language output,
// unless it's an ID that will be converted which needs to be formatted
// as an input (for chaining).
func (c *CommonFunctions) FormatReturnType(f introspection.Field, scopes ...string) (string, error) {
	return c.formatType(f.TypeRef, strings.Join(scopes, ""), c.ConvertID(f))
}

func (c *CommonFunctions) ToLowerCase(s string) string {
	return fmt.Sprintf("%c%s", unicode.ToLower(rune(s[0])), s[1:])
}

func (c *CommonFunctions) ToUpperCase(s string) string {
	return fmt.Sprintf("%c%s", unicode.ToUpper(rune(s[0])), s[1:])
}

func (c *CommonFunctions) IsListOfObject(t *introspection.TypeRef) bool {
	return t.OfType.OfType.IsObject()
}

func (c *CommonFunctions) GetArrayField(f *introspection.Field) ([]*introspection.Field, error) {
	schema := GetSchema()

	fieldType := f.TypeRef
	if !fieldType.IsOptional() {
		fieldType = fieldType.OfType
	}
	if !fieldType.IsList() {
		return nil, fmt.Errorf("field %s is not a list", f.Name)
	}
	fieldType = fieldType.OfType
	if !fieldType.IsOptional() {
		fieldType = fieldType.OfType
	}
	schemaType := schema.Types.Get(fieldType.Name)
	if schemaType == nil {
		return nil, fmt.Errorf("schema type %s is nil", fieldType.Name)
	}

	var fields []*introspection.Field
	var idField *introspection.Field
	// Only include scalar fields for now
	// TODO: include subtype too
	for _, typeField := range schemaType.Fields {
		if typeField.TypeRef.IsScalar() {
			fields = append(fields, typeField)
		}
		// TODO: hack to fix requesting all fields from list of id-able objects, need better solution
		if typeField.Name == "id" {
			idField = typeField
			break
		}
	}
	if idField != nil {
		return []*introspection.Field{idField}, nil
	}

	return fields, nil
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
	// NB: we only concern ourselves with the ID of the parent class, since this
	// is really only meant for ID and Sync, the only cases where we
	// intentionally return an ID (leaf node) instead of an object.
	return ref.Name == f.ParentObject.Name+"ID"
}

// FormatInputType formats a GraphQL type into the SDK language input
//
// Example: `String` -> `string`
func (c *CommonFunctions) FormatInputType(r *introspection.TypeRef, scopes ...string) (string, error) {
	return c.formatType(r, strings.Join(scopes, ""), true)
}

// FormatOutputType formats a GraphQL type into the SDK language output
//
// Example: `String` -> `string`
func (c *CommonFunctions) FormatOutputType(r *introspection.TypeRef, scopes ...string) (string, error) {
	return c.formatType(r, strings.Join(scopes, ""), false)
}

// formatType loops through the type reference to transform it into its SDK language.
func (c *CommonFunctions) formatType(r *introspection.TypeRef, scope string, input bool) (representation string, err error) {
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
				return c.formatTypeFuncs.FormatKindScalarString(representation), nil
			case introspection.ScalarInt:
				return c.formatTypeFuncs.FormatKindScalarInt(representation), nil
			case introspection.ScalarFloat:
				return c.formatTypeFuncs.FormatKindScalarFloat(representation), nil
			case introspection.ScalarBoolean:
				return c.formatTypeFuncs.FormatKindScalarBoolean(representation), nil
			default:
				return c.formatTypeFuncs.FormatKindScalarDefault(representation, scope+ref.Name, input), nil
			}
		case introspection.TypeKindObject:
			return scope + c.formatTypeFuncs.FormatKindObject(representation, ref.Name, input), nil
		case introspection.TypeKindInputObject:
			return scope + c.formatTypeFuncs.FormatKindInputObject(representation, ref.Name, input), nil
		case introspection.TypeKindEnum:
			return scope + c.formatTypeFuncs.FormatKindEnum(representation, ref.Name), nil
		}
	}

	return "", fmt.Errorf("unexpected type kind %s", r.Kind)
}
