package schema

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

// moduleIdentityTestSource reads a module source named name from an
// in-engine directory holding only its config, as a module's own files read
// from a workspace pinned to a commit are the same in every session.
func moduleIdentityTestSource(t *testing.T, e *transferTestEngine, ctx context.Context, name string) dagql.ObjectResult[*core.ModuleSource] {
	t.Helper()
	var src dagql.ObjectResult[*core.ModuleSource]
	require.NoError(t, e.dag.Select(ctx, e.dag.Root(), &src,
		dagql.Selector{Field: "directory"},
		dagql.Selector{Field: "withNewFile", Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.NewString("dagger.json")},
			{Name: "contents", Value: dagql.NewString(`{"name":"` + name + `"}`)},
		}},
		dagql.Selector{Field: "asModuleSource"}))
	return src
}

func moduleIdentityTestLoad(t *testing.T, e *transferTestEngine, ctx context.Context, src dagql.ObjectResult[*core.ModuleSource], selectors ...dagql.Selector) dagql.ObjectResult[*core.Module] {
	t.Helper()
	var mod dagql.ObjectResult[*core.Module]
	require.NoError(t, e.dag.Select(ctx, src, &mod, append(selectors, dagql.Selector{Field: "asModule"})...))
	return mod
}

// Two sessions that read a module source from the same content load one
// module: the later session's load is the earlier's. Before, every client
// read its own source and loaded its own module, so each session loaded
// every module again, on a warm engine as on a fresh one.
func TestModuleLoadedFromEqualSourcesIsShared(t *testing.T) {
	t.Parallel()
	e := newTransferTestEngine(t, transferTestSalt(t))

	first := e.session(t, "first")
	moduleIdentityTestLoad(t, e, first, moduleIdentityTestSource(t, e, first, "shared"))

	later := e.session(t, "later")
	mod := moduleIdentityTestLoad(t, e, later, moduleIdentityTestSource(t, e, later, "shared"))
	require.True(t, mod.HitCache(), "the later session's module is the earlier session's, not loaded again")
	require.Equal(t, "shared", mod.Self().Name())
}

// Sources that differ in what a module reads from them load different
// modules. withName changes only the module's name, which the
// implementation digest leaves out, so an identity built from it alone
// would hand the later session the earlier one's module.
func TestModuleLoadedFromSourcesWithDifferentNamesIsNotShared(t *testing.T) {
	t.Parallel()
	e := newTransferTestEngine(t, transferTestSalt(t))
	named := func(name string) dagql.Selector {
		return dagql.Selector{Field: "withName", Args: []dagql.NamedInput{{Name: "name", Value: dagql.NewString(name)}}}
	}

	first := e.session(t, "first")
	require.Equal(t, "alpha", moduleIdentityTestLoad(t, e, first, moduleIdentityTestSource(t, e, first, "base"), named("alpha")).Self().Name())

	later := e.session(t, "later")
	mod := moduleIdentityTestLoad(t, e, later, moduleIdentityTestSource(t, e, later, "base"), named("beta"))
	require.False(t, mod.HitCache(), "a module named otherwise is loaded, not taken from the earlier session")
	require.Equal(t, "beta", mod.Self().Name())
}

// A load that makes a call keyed per call, session or client reads what
// another session may see otherwise, so its module stays in its session: a
// later session loading the same source loads it again. A load that reads
// nothing of the sort is shared. The module's SDK makes the read here, as
// loading resolves a default-path context named by ref from the caller.
func TestModuleLoadReadingPerCallStaysInItsSession(t *testing.T) {
	t.Parallel()
	ctx, cache, srv, _ := prebuildTestEnv(t, "first")
	var probes atomic.Int32
	dagql.Fields[*core.Query]{
		dagql.Func("perCallProbe", func(context.Context, *core.Query, struct{}) (dagql.String, error) {
			probes.Add(1)
			return "read", nil
		}).WithInput(dagql.PerCallInput),
	}.Install(srv)
	readsPerCall := &prebuildTestSDK{runtime: func(ctx context.Context) error {
		var read dagql.String
		return srv.Select(ctx, srv.Root(), &read, dagql.Selector{Field: "perCallProbe"})
	}}

	later := withClient(ctx, "later", "later")
	for _, tc := range []struct {
		name   string
		sdk    *prebuildTestSDK
		shared bool
	}{
		{"reads-nothing", &prebuildTestSDK{}, true},
		{"reads-per-call", readsPerCall, false},
	} {
		_, err := selectAsModule(ctx, srv, prebuildTestSource(t, ctx, cache, srv, "first", tc.name, tc.sdk))
		require.NoError(t, err)
		mod, err := selectAsModule(later, srv, prebuildTestSource(t, later, cache, srv, "later", tc.name, tc.sdk))
		require.NoError(t, err)
		require.Equal(t, tc.shared, mod.HitCache(), "%s: the later session's module is the first's", tc.name)
	}
	require.EqualValues(t, 2, probes.Load(), "the module that read per call was loaded in each session")
}
