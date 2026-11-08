package dagql

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
)

// filledSchema returns a schema with every field of every part set, each
// part type once along any path. Its root operations, types, possible types
// and implementations share one definition, as a server's schema does.
func filledSchema() *ast.Schema {
	var fill func(v reflect.Value, onPath map[reflect.Type]bool)
	fill = func(v reflect.Value, onPath map[reflect.Type]bool) {
		switch v.Kind() {
		case reflect.String:
			v.SetString("s")
		case reflect.Bool:
			v.SetBool(true)
		case reflect.Int:
			v.SetInt(1)
		case reflect.Pointer:
			elem := v.Type().Elem()
			if onPath[elem] {
				return
			}
			onPath[elem] = true
			defer delete(onPath, elem)
			p := reflect.New(elem)
			fill(p.Elem(), onPath)
			v.Set(p)
		case reflect.Slice:
			s := reflect.MakeSlice(v.Type(), 1, 1)
			fill(s.Index(0), onPath)
			v.Set(s)
		case reflect.Struct:
			for i := range v.NumField() {
				fill(v.Field(i), onPath)
			}
		}
	}
	def := &ast.Definition{}
	fill(reflect.ValueOf(def).Elem(), map[reflect.Type]bool{reflect.TypeFor[ast.Definition](): true})
	directive := &ast.DirectiveDefinition{}
	fill(reflect.ValueOf(directive).Elem(), map[reflect.Type]bool{reflect.TypeFor[ast.DirectiveDefinition](): true})
	schema := &ast.Schema{
		Query:         def,
		Mutation:      def,
		Subscription:  def,
		Types:         map[string]*ast.Definition{"T": def},
		Directives:    map[string]*ast.DirectiveDefinition{"d": directive},
		PossibleTypes: map[string][]*ast.Definition{"T": {def}},
		Implements:    map[string][]*ast.Definition{"T": {def}},
		Description:   "s",
	}
	fill(reflect.ValueOf(&schema.SchemaDirectives).Elem(), map[reflect.Type]bool{})
	fill(reflect.ValueOf(&schema.Comment).Elem(), map[reflect.Type]bool{})
	return schema
}

// Every part of a schema changes its digest, and no position does. A field
// gqlparser adds to its AST fails this test until schemaDigest hashes it.
func TestSchemaDigestCoversEveryPart(t *testing.T) {
	t.Parallel()
	schema := filledSchema()
	base := schemaDigest(schema)
	require.Equal(t, base, schemaDigest(filledSchema()), "an equal schema built apart has the same digest")

	positionType := reflect.TypeFor[*ast.Position]()
	seen := map[uintptr]bool{}
	changed := 0
	var walk func(v reflect.Value, path string)
	walk = func(v reflect.Value, path string) {
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() || seen[v.Pointer()] {
				return
			}
			seen[v.Pointer()] = true
			walk(v.Elem(), path)
		case reflect.Slice:
			for i := range v.Len() {
				walk(v.Index(i), fmt.Sprintf("%s[%d]", path, i))
			}
		case reflect.Map:
			for iter := v.MapRange(); iter.Next(); {
				walk(iter.Value(), fmt.Sprintf("%s[%s]", path, iter.Key()))
			}
		case reflect.Struct:
			for i := range v.NumField() {
				field, part := v.Type().Field(i), v.Field(i)
				partPath := path + "." + field.Name
				require.True(t, field.IsExported(), "%s isn't exported, so this test can't check it", partPath)
				old := reflect.New(part.Type()).Elem()
				old.Set(part)
				if part.Type() == positionType {
					part.Set(reflect.ValueOf(&ast.Position{Line: 9}))
					require.Equal(t, base, schemaDigest(schema), "%s is a position", partPath)
					part.Set(old)
					continue
				}
				switch part.Kind() {
				case reflect.String:
					part.SetString(part.String() + "x")
				case reflect.Bool:
					part.SetBool(!part.Bool())
				case reflect.Int:
					part.SetInt(part.Int() + 1)
				case reflect.Pointer:
					if part.IsNil() {
						part.Set(reflect.New(part.Type().Elem()))
					} else {
						part.SetZero()
					}
				case reflect.Slice, reflect.Map:
					if part.Len() > 0 {
						part.SetZero()
					} else if part.Kind() == reflect.Slice {
						part.Set(reflect.MakeSlice(part.Type(), 1, 1))
					} else {
						t.Fatalf("%s: an empty map the test can't fill", partPath)
					}
				default:
					t.Fatalf("%s: a %s the test can't change", partPath, part.Kind())
				}
				require.NotEqual(t, base, schemaDigest(schema), "changing %s changes the digest", partPath)
				part.Set(old)
				changed++
				walk(part, partPath)
			}
		}
	}
	walk(reflect.ValueOf(schema), "Schema")
	require.Equal(t, base, schemaDigest(schema), "every change was undone")
	require.Greater(t, changed, 50)
}
