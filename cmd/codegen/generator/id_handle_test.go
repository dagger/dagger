package generator

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/cmd/codegen/introspection"
)

func TestIDHandleType(t *testing.T) {
	idRef := &introspection.TypeRef{
		Kind:   introspection.TypeKindNonNull,
		OfType: &introspection.TypeRef{Kind: introspection.TypeKindScalar, Name: "ID"},
	}
	expected := func(name string) introspection.Directives {
		value := `"` + name + `"`
		return introspection.Directives{{
			Name: "expectedType",
			Args: []*introspection.DirectiveArg{{Name: "name", Value: &value}},
		}}
	}
	llm := &introspection.Type{Kind: introspection.TypeKindObject, Name: "LLM"}
	fields := map[string]*introspection.Field{
		"id":     {Name: "id", TypeRef: idRef, Directives: expected("LLM"), ParentObject: llm},
		"sync":   {Name: "sync", TypeRef: idRef, Directives: expected("LLM"), ParentObject: llm},
		"spawn":  {Name: "spawn", TypeRef: idRef, Directives: expected("Agent"), ParentObject: llm},
		"opaque": {Name: "opaque", TypeRef: idRef, ParentObject: llm},
		"legacy": {
			Name: "legacy",
			TypeRef: &introspection.TypeRef{
				Kind:   introspection.TypeKindNonNull,
				OfType: &introspection.TypeRef{Kind: introspection.TypeKindScalar, Name: "LLMID"},
			},
			ParentObject: llm,
		},
	}

	for _, test := range []struct {
		field string
		want  string
	}{
		// the id field is never a handle
		{"id", ""},
		// the parent's own ID (sync-likes) loads the parent
		{"sync", "LLM"},
		// another type's ID loads that type
		{"spawn", "Agent"},
		// a bare ID is never a handle
		{"opaque", ""},
		// legacy FooID scalars only ever named the parent
		{"legacy", "LLM"},
	} {
		t.Run(test.field, func(t *testing.T) {
			funcs := NewCommonFunctions("v1.0.0", nil)
			require.Equal(t, test.want, funcs.IDHandleType(*fields[test.field]))
			require.Equal(t, test.want != "", funcs.ConvertID(*fields[test.field]))
		})
	}
}
