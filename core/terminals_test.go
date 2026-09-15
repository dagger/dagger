package core

import (
	"context"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func TestTerminalGroupRunRequiresOneTarget(t *testing.T) {
	t.Run("no target", func(t *testing.T) {
		err := (&TerminalGroup{}).Run(context.Background())
		require.EqualError(t, err, "no terminal targets selected")
	})

	t.Run("multiple targets", func(t *testing.T) {
		root := &ModTreeNode{}
		group := &TerminalGroup{Terminals: []*TerminalTarget{
			{Node: &ModTreeNode{Parent: root, Name: "first"}},
			{Node: &ModTreeNode{Parent: root, Name: "second"}},
		}}

		err := group.Run(context.Background())
		require.EqualError(t, err, "terminal selection matched 2 targets: first, second")
	})
}

func TestSupportsTerminal(t *testing.T) {
	dag := newTypeDefTestDag(t)
	objectType := func(name string, optional bool) dagql.ObjectResult[*TypeDef] {
		object := newTypeDefDetachedResult(t, dag, name+"Object", &ObjectTypeDef{Name: name})
		return newTypeDefDetachedResult(t, dag, name+"Type", &TypeDef{
			Kind:     TypeDefKindObject,
			Optional: optional,
			AsObject: dagql.NonNull(object),
		})
	}

	tests := map[string]struct {
		typeDef dagql.ObjectResult[*TypeDef]
		want    bool
	}{
		"container":          {typeDef: objectType("Container", false), want: true},
		"directory":          {typeDef: objectType("Directory", false), want: true},
		"optional container": {typeDef: objectType("Container", true), want: false},
		"other object":       {typeDef: objectType("File", false), want: false},
		"non-object": {
			typeDef: newTypeDefDetachedResult(t, dag, "stringType", &TypeDef{Kind: TypeDefKindString}),
			want:    false,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, test.want, supportsTerminal(&ModTreeNode{Type: test.typeDef}))
		})
	}
}
