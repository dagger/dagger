package dagql

import (
	"encoding/binary"

	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/zeebo/xxh3"

	"github.com/dagger/dagger/util/hashutil"
	"github.com/dagger/dagger/util/sortutil"
)

// schemaDigest hashes every part of schema its JSON encoding would hold,
// every list and map in a fixed order, straight into the hash: no encoding is
// built. Positions are left out: where a part sits in a source doesn't change
// the schema. Nor does a list or map being nil rather than empty, so the two
// hash the same. Each definition is hashed once; wherever it appears, in the
// types, the possible types, the implementations or the root operations, its
// hash stands for it.
func schemaDigest(schema *ast.Schema) digest.Digest {
	s := &schemaHasher{defs: map[*ast.Definition]xxh3.Uint128{}}
	h := xxh3.New()
	s.definition(h, schema.Query)
	s.definition(h, schema.Mutation)
	s.definition(h, schema.Subscription)
	s.directives(h, schema.SchemaDirectives)
	s.int(h, len(schema.Types))
	sortutil.RangeSorted(schema.Types, func(name string, def *ast.Definition) {
		s.str(h, name)
		s.definition(h, def)
	})
	s.int(h, len(schema.Directives))
	sortutil.RangeSorted(schema.Directives, func(name string, def *ast.DirectiveDefinition) {
		s.str(h, name)
		s.directiveDefinition(h, def)
	})
	for _, m := range []map[string][]*ast.Definition{schema.PossibleTypes, schema.Implements} {
		s.int(h, len(m))
		sortutil.RangeSorted(m, func(name string, defs []*ast.Definition) {
			s.str(h, name)
			s.int(h, len(defs))
			for _, def := range defs {
				s.definition(h, def)
			}
		})
	}
	s.str(h, schema.Description)
	s.comments(h, schema.Comment)
	return digest.NewDigest(hashutil.XXH3, h)
}

// schemaHasher writes a schema's parts into hashers. Every value is written
// with its length or a presence flag first, so no two schemas write the same
// bytes.
type schemaHasher struct {
	// defs holds each definition's hash once it's computed. A definition
	// that refers back to itself, through a value or a directive, writes the
	// zero hash for the reference.
	defs map[*ast.Definition]xxh3.Uint128
	// free holds the hashers of finished definitions, for the next.
	free []*xxh3.Hasher
	buf  [16]byte
}

func (s *schemaHasher) int(h *xxh3.Hasher, n int) {
	binary.LittleEndian.PutUint64(s.buf[:8], uint64(n))
	h.Write(s.buf[:8])
}

func (s *schemaHasher) bool(h *xxh3.Hasher, b bool) {
	if b {
		s.int(h, 1)
	} else {
		s.int(h, 0)
	}
}

func (s *schemaHasher) str(h *xxh3.Hasher, v string) {
	s.int(h, len(v))
	h.WriteString(v)
}

func (s *schemaHasher) strs(h *xxh3.Hasher, vs []string) {
	s.int(h, len(vs))
	for _, v := range vs {
		s.str(h, v)
	}
}

// present writes whether a pointer is set, and returns it.
func (s *schemaHasher) present(h *xxh3.Hasher, set bool) bool {
	s.bool(h, set)
	return set
}

// definition writes def's hash, computing it the first time.
func (s *schemaHasher) definition(h *xxh3.Hasher, def *ast.Definition) {
	if !s.present(h, def != nil) {
		return
	}
	sum, ok := s.defs[def]
	if !ok {
		s.defs[def] = xxh3.Uint128{}
		var dh *xxh3.Hasher
		if n := len(s.free); n > 0 {
			dh, s.free = s.free[n-1], s.free[:n-1]
			dh.Reset()
		} else {
			dh = xxh3.New()
		}
		s.str(dh, string(def.Kind))
		s.str(dh, def.Description)
		s.str(dh, def.Name)
		s.directives(dh, def.Directives)
		s.strs(dh, def.Interfaces)
		s.int(dh, len(def.Fields))
		for _, field := range def.Fields {
			s.field(dh, field)
		}
		s.strs(dh, def.Types)
		s.int(dh, len(def.EnumValues))
		for _, value := range def.EnumValues {
			s.enumValue(dh, value)
		}
		s.bool(dh, def.BuiltIn)
		s.comments(dh, def.BeforeDescriptionComment)
		s.comments(dh, def.AfterDescriptionComment)
		s.comments(dh, def.EndOfDefinitionComment)
		sum = dh.Sum128()
		s.free = append(s.free, dh)
		s.defs[def] = sum
	}
	binary.LittleEndian.PutUint64(s.buf[:8], sum.Lo)
	binary.LittleEndian.PutUint64(s.buf[8:], sum.Hi)
	h.Write(s.buf[:])
}

func (s *schemaHasher) field(h *xxh3.Hasher, field *ast.FieldDefinition) {
	if !s.present(h, field != nil) {
		return
	}
	s.str(h, field.Description)
	s.str(h, field.Name)
	s.argumentDefinitions(h, field.Arguments)
	s.value(h, field.DefaultValue)
	s.typ(h, field.Type)
	s.directives(h, field.Directives)
	s.comments(h, field.BeforeDescriptionComment)
	s.comments(h, field.AfterDescriptionComment)
}

func (s *schemaHasher) argumentDefinitions(h *xxh3.Hasher, args ast.ArgumentDefinitionList) {
	s.int(h, len(args))
	for _, arg := range args {
		if !s.present(h, arg != nil) {
			continue
		}
		s.str(h, arg.Description)
		s.str(h, arg.Name)
		s.value(h, arg.DefaultValue)
		s.typ(h, arg.Type)
		s.directives(h, arg.Directives)
		s.comments(h, arg.BeforeDescriptionComment)
		s.comments(h, arg.AfterDescriptionComment)
	}
}

func (s *schemaHasher) enumValue(h *xxh3.Hasher, value *ast.EnumValueDefinition) {
	if !s.present(h, value != nil) {
		return
	}
	s.str(h, value.Description)
	s.str(h, value.Name)
	s.directives(h, value.Directives)
	s.comments(h, value.BeforeDescriptionComment)
	s.comments(h, value.AfterDescriptionComment)
}

func (s *schemaHasher) directiveDefinition(h *xxh3.Hasher, def *ast.DirectiveDefinition) {
	if !s.present(h, def != nil) {
		return
	}
	s.str(h, def.Description)
	s.str(h, def.Name)
	s.argumentDefinitions(h, def.Arguments)
	s.int(h, len(def.Locations))
	for _, location := range def.Locations {
		s.str(h, string(location))
	}
	s.bool(h, def.IsRepeatable)
	s.comments(h, def.BeforeDescriptionComment)
	s.comments(h, def.AfterDescriptionComment)
}

func (s *schemaHasher) directives(h *xxh3.Hasher, directives ast.DirectiveList) {
	s.int(h, len(directives))
	for _, directive := range directives {
		if !s.present(h, directive != nil) {
			continue
		}
		s.str(h, directive.Name)
		s.int(h, len(directive.Arguments))
		for _, arg := range directive.Arguments {
			if !s.present(h, arg != nil) {
				continue
			}
			s.str(h, arg.Name)
			s.value(h, arg.Value)
			s.comments(h, arg.Comment)
		}
		s.definition(h, directive.ParentDefinition)
		s.directiveDefinition(h, directive.Definition)
		s.str(h, string(directive.Location))
	}
}

func (s *schemaHasher) value(h *xxh3.Hasher, value *ast.Value) {
	if !s.present(h, value != nil) {
		return
	}
	s.str(h, value.Raw)
	s.int(h, len(value.Children))
	for _, child := range value.Children {
		if !s.present(h, child != nil) {
			continue
		}
		s.str(h, child.Name)
		s.value(h, child.Value)
		s.comments(h, child.Comment)
	}
	s.int(h, int(value.Kind))
	s.comments(h, value.Comment)
	s.definition(h, value.Definition)
	if vd := value.VariableDefinition; s.present(h, vd != nil) {
		s.str(h, vd.Variable)
		s.typ(h, vd.Type)
		s.value(h, vd.DefaultValue)
		s.directives(h, vd.Directives)
		s.comments(h, vd.Comment)
		s.definition(h, vd.Definition)
		s.bool(h, vd.Used)
	}
	s.typ(h, value.ExpectedType)
	s.bool(h, value.ExpectedTypeHasDefault)
}

func (s *schemaHasher) typ(h *xxh3.Hasher, t *ast.Type) {
	for s.present(h, t != nil) {
		s.str(h, t.NamedType)
		s.bool(h, t.NonNull)
		t = t.Elem
	}
}

func (s *schemaHasher) comments(h *xxh3.Hasher, group *ast.CommentGroup) {
	if !s.present(h, group != nil) {
		return
	}
	s.int(h, len(group.List))
	for _, comment := range group.List {
		if s.present(h, comment != nil) {
			s.str(h, comment.Value)
		}
	}
}
