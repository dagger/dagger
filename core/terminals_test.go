package core

import (
	"context"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func TestTerminalGroupRunRequiresOneTarget(t *testing.T) {
	t.Run("no target", func(t *testing.T) {
		err := (&TerminalGroup{}).Run(context.Background(), TerminalSetupArgs{})
		require.EqualError(t, err, "no terminal targets selected")
	})

	t.Run("multiple targets", func(t *testing.T) {
		root := &ModTreeNode{}
		group := &TerminalGroup{Terminals: []*TerminalTarget{
			{Node: &ModTreeNode{Parent: root, Name: "first"}},
			{Node: &ModTreeNode{Parent: root, Name: "second"}},
		}}

		err := group.Run(context.Background(), TerminalSetupArgs{})
		require.EqualError(t, err, "terminal selection matched 2 targets: first, second")
	})
}

func TestTerminalGroupSelectsDefault(t *testing.T) {
	dag := newTypeDefTestDag(t)
	objectType := func(name string) dagql.ObjectResult[*TypeDef] {
		object := newTypeDefDetachedResult(t, dag, name+"Object", &ObjectTypeDef{Name: name})
		return newTypeDefDetachedResult(t, dag, name+"Type", &TypeDef{Kind: TypeDefKindObject, AsObject: dagql.NonNull(object)})
	}
	ctr, dir := objectType("Container"), objectType("Directory")
	root := &ModTreeNode{}
	entrypoint := &ModTreeNode{Parent: root, Name: "entry", WorkspaceEntrypoint: true}
	other := &ModTreeNode{Parent: root, Name: "other"}
	target := func(parent *ModTreeNode, name string, typeDef dagql.ObjectResult[*TypeDef]) *TerminalTarget {
		return &TerminalTarget{Node: &ModTreeNode{Parent: parent, Name: name, Type: typeDef}}
	}

	t.Run("only container", func(t *testing.T) {
		want := target(other, "ctr", ctr)
		got, err := (&TerminalGroup{Terminals: []*TerminalTarget{target(other, "src", dir), want}}).selected()
		require.NoError(t, err)
		require.Same(t, want, got)
	})

	t.Run("only container in entrypoint", func(t *testing.T) {
		want := target(entrypoint, "ctr", ctr)
		got, err := (&TerminalGroup{Terminals: []*TerminalTarget{target(other, "ctr", ctr), want}}).selected()
		require.NoError(t, err)
		require.Same(t, want, got)
	})

	t.Run("no default", func(t *testing.T) {
		_, err := (&TerminalGroup{Terminals: []*TerminalTarget{target(entrypoint, "a", ctr), target(entrypoint, "b", ctr)}}).selected()
		require.EqualError(t, err, "terminal selection matched 2 targets: a, b")
	})
}

func TestTerminalType(t *testing.T) {
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
		want    string
	}{
		"container":          {typeDef: objectType("Container", false), want: "Container"},
		"directory":          {typeDef: objectType("Directory", false), want: "Directory"},
		"optional container": {typeDef: objectType("Container", true), want: ""},
		"other object":       {typeDef: objectType("File", false), want: ""},
		"non-object": {
			typeDef: newTypeDefDetachedResult(t, dag, "stringType", &TypeDef{Kind: TypeDefKindString}),
			want:    "",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, test.want, terminalType(&ModTreeNode{Type: test.typeDef}))
		})
	}
}
