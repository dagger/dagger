package generator

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/cmd/codegen/introspection"
)

func TestSupportsIDHandles(t *testing.T) {
	for _, test := range []struct {
		version string
		want    bool
	}{
		{"", true},
		{"development", true},
		{"v0.21.0-dev", false},
		{"v1.0.0-beta.11", false},
		{"v1.0.0-beta.11-dev", false},
		{"v1.0.0-beta.12", true},
		{"v1.0.0-beta.12-dev", true},
		{"v1.0.0-rc.1", true},
		{"v1.0.0", true},
	} {
		t.Run(test.version, func(t *testing.T) {
			require.Equal(t, test.want, SupportsIDHandles(test.version))
		})
	}
}

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
		version string
		field   string
		want    string
	}{
		// the id field is never a handle
		{"v1.0.0", "id", ""},
		// the parent's own ID (sync-likes) has always been loaded
		{"v1.0.0-beta.11", "sync", "LLM"},
		{"v1.0.0", "sync", "LLM"},
		// another type's ID is loaded from the cutover on
		{"v1.0.0-beta.11", "spawn", ""},
		{"v1.0.0", "spawn", "Agent"},
		// a bare ID is never a handle
		{"v1.0.0", "opaque", ""},
		// legacy FooID scalars only ever named the parent
		{"v0.21.0", "legacy", "LLM"},
	} {
		t.Run(test.version+"/"+test.field, func(t *testing.T) {
			funcs := NewCommonFunctions(test.version, nil)
			require.Equal(t, test.want, funcs.IDHandleType(*fields[test.field]))
			require.Equal(t, test.want != "", funcs.ConvertID(*fields[test.field]))
		})
	}
}
