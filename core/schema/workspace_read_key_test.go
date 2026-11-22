package schema

import (
	"slices"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

// TestWorkspaceReadCacheKey asserts how each workspace read is keyed and
// declared, through the fields as core's schema installs them. A read of a Git
// workspace with no edits and no mounts follows from its inputs when its
// result is a function of the files it reads, since it reads only its commit's
// files through a tree that discards .git. A search, whose results come in the
// order ripgrep finds them, doesn't, nor does a read of any other workspace.
// Only a workspace without a content identity keys its reads per client.
func TestWorkspaceReadCacheKey(t *testing.T) {
	t.Parallel()
	e := newTransferTestEngine(t, nil)
	ctx := e.session(t, "reader")
	workspaceType, ok := e.dag.ObjectType("Workspace")
	require.True(t, ok)

	synthetic := func(op string, typed dagql.Typed) *dagql.ResultCall {
		return &dagql.ResultCall{Kind: dagql.ResultCallKindSynthetic, SyntheticOp: op, Type: dagql.NewResultCallType(typed.Type())}
	}
	mounts, err := dagql.NewObjectResultForCall(&core.Directory{}, e.dag, synthetic("mounts", &core.Directory{}))
	require.NoError(t, err)

	gitWorkspace := func() *core.Workspace {
		ws := &core.Workspace{ClientID: "loader"}
		ws.SetSource(core.NewWorkspaceSourceGitRef(dagql.Result[*core.GitRef]{}))
		return ws
	}
	hostWorkspace := &core.Workspace{ClientID: "loader"}
	hostWorkspace.SetHostPath("/src")
	hostWorkspace.SetSource(core.NewWorkspaceSourceClientLocal("/src"))
	directoryWorkspace := &core.Workspace{}
	directoryWorkspace.SetSource(core.NewWorkspaceSourceDirectory(mounts))

	reads := []struct {
		field string
		args  map[string]dagql.Input
		files bool
	}{
		{field: "directory", args: map[string]dagql.Input{"path": dagql.NewString(".")}, files: true},
		{field: "file", args: map[string]dagql.Input{"path": dagql.NewString("go.mod")}, files: true},
		{field: "glob", args: map[string]dagql.Input{"pattern": dagql.NewString("*")}, files: true},
		{field: "findUp", args: map[string]dagql.Input{"name": dagql.NewString("go.mod")}, files: true},
		{field: "findRoots", args: map[string]dagql.Input{"markers": dagql.ArrayInput[dagql.String]{"go.mod"}}, files: true},
		{field: "git", args: map[string]dagql.Input{}, files: true},
		{field: "search", args: map[string]dagql.Input{"pattern": dagql.NewString("x")}},
	}
	for _, tc := range []struct {
		name      string
		ws        *core.Workspace
		follows   bool
		perClient bool
	}{
		{name: "git", ws: gitWorkspace(), follows: true},
		{name: "git with mounts", ws: gitWorkspace().WithMountsDir(mounts)},
		{name: "directory", ws: directoryWorkspace},
		{name: "host", ws: hostWorkspace, perClient: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent, err := dagql.NewObjectResultForCall(tc.ws, e.dag, synthetic("workspace-"+tc.name, tc.ws))
			require.NoError(t, err)
			for _, read := range reads {
				spec, ok := workspaceType.FieldSpec(read.field, e.dag.View)
				require.True(t, ok, read.field)
				require.NotNil(t, spec.GetDynamicInput, read.field)
				req := &dagql.CallRequest{ResultCall: &dagql.ResultCall{}}
				require.NoError(t, spec.GetDynamicInput(ctx, parent, read.args, e.dag.View, req), read.field)
				require.Equal(t, tc.follows && read.files, req.ResultCall.FollowsFromInputs, "%s: follows from its inputs", read.field)
				require.Equal(t, !tc.perClient, req.IsPersistable, "%s: kept past the session", read.field)
				require.Equal(t, tc.perClient, slices.ContainsFunc(req.ImplicitInputs, func(input *dagql.ResultCallArg) bool {
					return input.Name == dagql.PerClientInput.Name
				}), "%s: keyed per client", read.field)
			}
		})
	}
}
