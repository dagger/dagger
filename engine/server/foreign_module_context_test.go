package server

import (
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func TestForeignModuleContextReaders(t *testing.T) {
	native := &core.ModuleSource{Kind: core.ModuleSourceKindLocal, ModuleName: "probe", Local: &core.LocalModuleSource{ContextDirectoryPath: "/producer"}}
	foreign := native.Clone()
	foreign.Local.Foreign = true
	_, err := pendingRelatedModule(dagql.ObjectResult[*core.ModuleSource]{}, foreign, nil, false)
	require.ErrorIs(t, err, core.ErrForeignModuleContext)
	srv, err := dagql.NewServer(t.Context(), &core.Query{})
	require.NoError(t, err)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*core.ModuleSource]{}))
	source, err := dagql.NewObjectResultForCall(foreign, srv, &dagql.ResultCall{Kind: dagql.ResultCallKindSynthetic, SyntheticOp: "context", Type: dagql.NewResultCallType(foreign.Type())})
	require.NoError(t, err)
	_, err = pendingRelatedModule(source, native, nil, true)
	require.ErrorIs(t, err, core.ErrForeignModuleContext)
	pending, err := pendingRelatedModule(dagql.ObjectResult[*core.ModuleSource]{}, native, nil, false)
	require.NoError(t, err)
	require.Equal(t, "/producer", pending.Ref)
	require.True(t, isCoreRootField("_remoteCacheFixture"))
}
