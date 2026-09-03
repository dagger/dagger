package generator

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/dagger/dagger/cmd/codegen/introspection"
	"golang.org/x/mod/semver"
)

const nullableObjectSDKCutoverVersion = "v1.0.0-beta.10"

// idHandleSDKCutoverVersion is the first schema version whose SDKs load
// every ID-returning field that carries an @expectedType directive as the
// object it names. Older views only convert fields returning their parent's
// own ID (the sync-like shape) and hand back the raw ID otherwise.
const idHandleSDKCutoverVersion = "v1.0.0-beta.12"

var betaVersion = regexp.MustCompile(`^(v\d+\.\d+\.\d+-beta\.\d+)`)

const (
	QueryStructName       = "Query"
	QueryStructClientName = "Client"
)

// FormatTypeFuncs is an interface to format any GraphQL type.
// Each generator has to implement this interface.
type FormatTypeFuncs interface {
	WithScope(scope string) FormatTypeFuncs

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
	schemaVersion   string
	formatTypeFuncs FormatTypeFuncs
}

func NewCommonFunctions(schemaVersion string, formatTypeFuncs FormatTypeFuncs) *CommonFunctions {
	return &CommonFunctions{schemaVersion: schemaVersion, formatTypeFuncs: formatTypeFuncs}
}

// IsSelfChainable returns true if an object type has any fields that return
// that same type, and does not already have a field named "with" (which would
// conflict with the generated With helper method).
func (c *CommonFunctions) IsSelfChainable(t introspection.Type) bool {
	selfChainable := false
	for _, f := range t.Fields {
		// If there's already a "with" field, the generated With helper
		// would conflict with it — skip generating the helper.
		if f.Name == "with" {
			return false
		}
		// Only consider fields that return a non-null object.
		if !f.TypeRef.IsObject() || f.TypeRef.Kind != introspection.TypeKindNonNull {
			continue
		}
		if f.TypeRef.OfType.Name == t.Name {
			selfChainable = true
		}
	}
	return selfChainable
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
	case introspection.TypeKindObject, introspection.TypeKindInterface:
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
	case introspection.TypeKindInterface:
		// Interfaces are always IDable (they represent objects that implement them).
		return true, nil
	default:
		return false, nil
	}
}

// FormatReturnType formats a GraphQL type into the SDK language output,
// unless it's an ID that will be converted which needs to be formatted
// as an input (for chaining).
func (c *CommonFunctions) FormatReturnType(f introspection.Field, scopes ...string) (string, error) {
	if handle := c.IDHandleType(f); handle != "" {
		// An ID handle is returned as the object it loads (e.g. sync
		// returns the parent, LLM.spawn returns an Agent).
		scope := strings.Join(scopes, "")
		return c.formatTypeFuncs.WithScope(scope).FormatKindObject("", handle, false), nil
	}
	return c.formatType(f.TypeRef, strings.Join(scopes, ""), false)
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

func (c *CommonFunctions) IsListOfEnum(t *introspection.TypeRef) bool {
	return t.OfType.OfType.IsEnum()
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
// converted into an object: see IDHandleType.
func (c *CommonFunctions) ConvertID(f introspection.Field) bool {
	return c.IDHandleType(f) != ""
}

// IDHandleType returns the name of the object an ID-returning field loads
// in the SDK, or "" when the field hands back the ID as-is (including the
// id field itself).
//
// The @expectedType directive names the object. Fields returning their
// parent's own ID (sync-likes) have always been loaded as the parent, and
// from idHandleSDKCutoverVersion on every expected type is loaded, so a
// field like LLM.spawn returns an Agent rather than an Agent's ID. Older
// views keep the parent-only rule so their generated signatures do not
// move, and fall back to the legacy FooID scalar suffix convention.
func (c *CommonFunctions) IDHandleType(f introspection.Field) string {
	if f.Name == "id" {
		return ""
	}
	ref := f.TypeRef
	if ref.Kind == introspection.TypeKindNonNull {
		ref = ref.OfType
	}
	if ref.Kind != introspection.TypeKindScalar {
		return ""
	}
	if expectedType := f.Directives.ExpectedType(); expectedType != "" {
		if expectedType == f.ParentObject.Name || SupportsIDHandles(c.schemaVersion) {
			return expectedType
		}
		return ""
	}
	// Legacy fallback: check FooID suffix pattern.
	if ref.Name == f.ParentObject.Name+"ID" {
		return f.ParentObject.Name
	}
	return ""
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
	ff := c.formatTypeFuncs.WithScope(scope)

	for ref := r; ref != nil; ref = ref.OfType {
		switch ref.Kind {
		case introspection.TypeKindList:
			// Handle this special case with defer to format array at the end of
			// the loop.
			// Since an SDK needs to insert it at the end, other at the beginning.
			defer func() {
				representation = ff.FormatKindList(representation)
			}()
		case introspection.TypeKindScalar:
			switch introspection.Scalar(ref.Name) {
			case introspection.ScalarString:
				return ff.FormatKindScalarString(representation), nil
			case introspection.ScalarInt:
				return ff.FormatKindScalarInt(representation), nil
			case introspection.ScalarFloat:
				return ff.FormatKindScalarFloat(representation), nil
			case introspection.ScalarBoolean:
				return ff.FormatKindScalarBoolean(representation), nil
			default:
				return ff.FormatKindScalarDefault(representation, ref.Name, input), nil
			}
		case introspection.TypeKindObject, introspection.TypeKindInterface:
			return ff.FormatKindObject(representation, ref.Name, input), nil
		case introspection.TypeKindInputObject:
			return ff.FormatKindInputObject(representation, ref.Name, input), nil
		case introspection.TypeKindEnum:
			return ff.FormatKindEnum(representation, ref.Name), nil
		}
	}

	return "", fmt.Errorf("unexpected type kind %s", r.Kind)
}

func (c *CommonFunctions) CheckVersionCompatibility(minVersion string) bool {
	return semver.Compare(c.schemaVersion, minVersion) >= 0
}

func SupportsNullableObjects(schemaVersion string) bool {
	return schemaVersionAtLeast(schemaVersion, nullableObjectSDKCutoverVersion)
}

// SupportsIDHandles reports whether SDKs generated for schemaVersion load
// every @expectedType-annotated ID return as its object (see IDHandleType).
func SupportsIDHandles(schemaVersion string) bool {
	return schemaVersionAtLeast(schemaVersion, idHandleSDKCutoverVersion)
}

// schemaVersionAtLeast compares a schema version against a feature cutover.
// Unknown or non-semver versions (development builds) get every feature;
// a beta prerelease is compared by its beta number, ignoring any dev suffix.
func schemaVersionAtLeast(schemaVersion, cutover string) bool {
	if schemaVersion == "" || !semver.IsValid(schemaVersion) {
		return true
	}
	if version := betaVersion.FindString(schemaVersion); version != "" {
		schemaVersion = version
	}
	return semver.Compare(schemaVersion, cutover) >= 0
}
