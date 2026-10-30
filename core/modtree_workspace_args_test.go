package core

import (
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func TestBoundWorkspaceArgsKeepCallerArgs(t *testing.T) {
	dag := newTypeDefTestDag(t)
	dag.InstallObject(dagql.NewClass(dag, dagql.ClassOpts[*Workspace]{Typed: &Workspace{}}))
	wsObj := newTypeDefDetachedResult(t, dag, "workspaceType", NewObjectTypeDef("Workspace", "", nil))
	wsType := (&TypeDef{}).WithObjectTypeDef(wsObj)
	strType := (&TypeDef{}).WithKind(TypeDefKindString)
	arg := func(name string, typeDef *TypeDef) dagql.ObjectResult[*FunctionArg] {
		return newTypeDefDetachedResult(t, dag, "arg-"+name, &FunctionArg{
			Name: name, OriginalName: name,
			TypeDef: newTypeDefDetachedResult(t, dag, "type-"+name, typeDef),
		})
	}
	fn := &Function{Name: "f", OriginalName: "f", Args: dagql.ObjectResultArray[*FunctionArg]{
		arg("ws", wsType),
		arg("other", wsType),
		arg("optional", wsType.WithOptional(true)),
		arg("name", strType),
	}}

	cache, err := dagql.NewCache(t.Context(), "", nil, nil)
	require.NoError(t, err)
	ctx := dagql.ContextWithCache(t.Context(), cache)
	bound := newTypeDefAttachedResult(t, ctx, cache, dag, "boundWorkspace", &Workspace{})
	ctx = WorkspaceToContext(ctx, bound)
	boundID, err := bound.ID()
	require.NoError(t, err)

	names := func(args []dagql.NamedInput) []string {
		var out []string
		for _, arg := range args {
			out = append(out, arg.Name)
			id, ok := arg.Value.(dagql.IDable)
			require.True(t, ok, "argument %s is not an ID", arg.Name)
			argID, err := id.ID()
			require.NoError(t, err)
			require.Equal(t, boundID.EngineResultID(), argID.EngineResultID(), "argument %s", arg.Name)
		}
		return out
	}

	// No caller arguments: every required Workspace argument is bound, and
	// the optional one is left to dagql's injection hook.
	args, err := boundWorkspaceArgs(ctx, nil, fn, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"ws", "other"}, names(args))

	// A caller-supplied Workspace wins: the bound one is not added alongside
	// it, where dagql would pick whichever comes first.
	explicit := dagql.String("explicit")
	args, err = boundWorkspaceArgs(ctx, nil, fn, []dagql.NamedInput{{Name: "ws", Value: explicit}})
	require.NoError(t, err)
	require.Equal(t, []string{"other"}, names(args))

	args, err = boundWorkspaceArgs(ctx, nil, fn, []dagql.NamedInput{
		{Name: "ws", Value: explicit}, {Name: "other", Value: explicit},
	})
	require.NoError(t, err)
	require.Empty(t, args)
}
