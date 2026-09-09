package schema

import (
	"context"
	"errors"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNestingLegacyTerminalForwarding(t *testing.T) {
	for _, tc := range []struct {
		name string
		arg  []dagql.NamedInput
		want bool
	}{
		{name: "omitted"},
		{name: "disabled", arg: []dagql.NamedInput{{Name: "experimentalPrivilegedNesting", Value: dagql.Opt(dagql.Boolean(false))}}},
		{name: "enabled", arg: []dagql.NamedInput{{Name: "experimentalPrivilegedNesting", Value: dagql.Opt(dagql.Boolean(true))}}, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, dag := newNestingTestServer(t, "v0.11.0")
			obj, ok := dag.ObjectType("Container")
			require.True(t, ok)
			field, ok := obj.FieldSpec("terminal", "v0.12.0")
			require.True(t, ok)
			stopped := errors.New("terminal forwarding inspected")
			called := false
			// Intercept the terminal attach boundary while retaining the real
			// schema arguments and version filter. No interactive client is needed.
			obj.Extend(field, func(ctx context.Context, _ dagql.AnyResult, inputs map[string]dagql.Input) (dagql.AnyResult, error) {
				called = true
				assert.False(t, core.Supports(ctx, defaultNestingVersion))
				var args containerTerminalArgs
				require.NoError(t, field.Args.Decode(inputs, &args, dagql.CurrentCall(ctx).View))
				assert.Equal(t, tc.want, args.ExperimentalPrivilegedNesting.GetOr(false).Bool())
				return nil, stopped
			})
			var terminal dagql.ObjectResult[*core.TerminalLegacy]
			err := dag.Select(ctx, dag.Root(), &terminal,
				dagql.Selector{Field: "container"},
				dagql.Selector{Field: "terminal", View: "v0.11.0", Args: tc.arg},
			)
			require.ErrorContains(t, err, stopped.Error())
			require.True(t, called)
		})
	}
}

func TestNestingLegacyOutputForwarding(t *testing.T) {
	for _, output := range []string{"stdout", "stderr"} {
		t.Run(output, func(t *testing.T) {
			ctx, dag := newNestingTestServer(t, "v0.11.0")
			obj, ok := dag.ObjectType("Container")
			require.True(t, ok)
			field, ok := obj.(dagql.Class[*core.Container]).Field("withExec", "v0.12.0")
			require.True(t, ok)
			stopped := errors.New("implicit exec inspected")
			called := false
			obj.Extend(*field.Spec, func(ctx context.Context, self dagql.AnyResult, inputs map[string]dagql.Input) (dagql.AnyResult, error) {
				called = true
				// Run the real resolver and inspect its execution plan without
				// starting a container or requiring a BuildKit backend.
				res, err := field.Func(ctx, self.(dagql.ObjectResult[*core.Container]), inputs, dagql.CurrentCall(ctx).View)
				require.NoError(t, err)
				ctr := res.(dagql.ObjectResult[*core.Container])
				lazy, ok := ctr.Self().Lazy.(*core.ContainerExecLazy)
				require.True(t, ok)
				assert.False(t, lazy.State.Opts.ExperimentalPrivilegedNesting)
				assert.True(t, lazy.State.Opts.UseEntrypoint)
				return nil, stopped
			})
			var result dagql.String
			err := dag.Select(ctx, dag.Root(), &result,
				dagql.Selector{Field: "container"},
				dagql.Selector{Field: output, View: "v0.11.0"},
			)
			require.ErrorContains(t, err, stopped.Error())
			require.True(t, called)
		})
	}
}

func newNestingTestServer(t *testing.T, view call.View) (context.Context, *dagql.Server) {
	t.Helper()
	ctx := t.Context()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close(context.Background())) })
	ctx = dagql.ContextWithCache(ctx, cache)
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{ClientID: "nesting-call", SessionID: "nesting-call"})
	srv := &currentTypeDefsTestServer{platform: core.Platform{OS: "linux", Architecture: "amd64"}}
	base, err := NewCoreSchemaBase(ctx, srv)
	require.NoError(t, err)
	dag, err := base.Fork(ctx, core.NewRoot(srv), view)
	require.NoError(t, err)
	srv.dag = dag
	return ctx, dag
}

// Internal calls use the latest API even on a legacy server.
func TestNestingExecCallView(t *testing.T) {
	ctx, dag := newNestingTestServer(t, "v0.21.0")
	for _, tc := range []struct {
		name string
		view call.View
		arg  []dagql.NamedInput
		want bool
	}{
		{name: "internal default", want: true},
		{name: "internal opt-out", arg: []dagql.NamedInput{{Name: "disableNesting", Value: dagql.Boolean(true)}}},
		{name: "legacy default", view: "v0.21.0"},
		{name: "legacy opt-in", view: "v0.21.0", arg: []dagql.NamedInput{{Name: "experimentalPrivilegedNesting", Value: dagql.Boolean(true)}}, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var ctr dagql.ObjectResult[*core.Container]
			require.NoError(t, dag.Select(ctx, dag.Root(), &ctr,
				dagql.Selector{Field: "container"},
				dagql.Selector{Field: "withExec", View: tc.view, Args: append([]dagql.NamedInput{
					{Name: "args", Value: dagql.ArrayInput[dagql.String]{"true"}},
				}, tc.arg...)},
			))
			lazy, ok := ctr.Self().Lazy.(*core.ContainerExecLazy)
			require.True(t, ok)
			require.Equal(t, tc.want, lazy.State.Opts.ExperimentalPrivilegedNesting)
		})
	}
}

func TestNestingTerminalDefaultsCallView(t *testing.T) {
	ctx, dag := newNestingTestServer(t, "v0.21.0")
	for _, tc := range []struct {
		name string
		view call.View
		arg  []dagql.NamedInput
		want bool
	}{
		{name: "internal default", want: true},
		{name: "internal opt-out", arg: []dagql.NamedInput{{Name: "disableNesting", Value: dagql.Boolean(true)}}},
		{name: "legacy default", view: "v0.21.0"},
		{name: "legacy opt-in", view: "v0.21.0", arg: []dagql.NamedInput{{Name: "experimentalPrivilegedNesting", Value: dagql.Opt(dagql.Boolean(true))}}, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var ctr dagql.ObjectResult[*core.Container]
			require.NoError(t, dag.Select(ctx, dag.Root(), &ctr,
				dagql.Selector{Field: "container"},
				dagql.Selector{Field: "withDefaultTerminalCmd", View: tc.view, Args: append([]dagql.NamedInput{
					{Name: "args", Value: dagql.ArrayInput[dagql.String]{"sh"}},
				}, tc.arg...)},
			))
			require.Equal(t, tc.want, ctr.Self().DefaultTerminalCmd.ExperimentalPrivilegedNesting.GetOr(false).Bool())
		})
	}
}

func TestNestingSchemaVersions(t *testing.T) {
	for _, tc := range []struct {
		version string
		present string
		absent  string
	}{
		{version: "v0.21.0", present: "experimentalPrivilegedNesting", absent: "disableNesting"},
		{version: "v1.0.0-beta.11", present: "experimentalPrivilegedNesting", absent: "disableNesting"},
		{version: "v1.0.0-beta.12", present: "disableNesting", absent: "experimentalPrivilegedNesting"},
		{version: "v1.0.0", present: "disableNesting", absent: "experimentalPrivilegedNesting"},
	} {
		t.Run(tc.version, func(t *testing.T) {
			_, dag := newNestingTestServer(t, call.View(tc.version))
			data, err := getSchemaJSON(nil, nil, dag.View, dag)
			require.NoError(t, err)
			schema := decodeSchemaResponse(t, data).Schema
			for _, target := range [][2]string{
				{"Container", "withExec"}, {"Container", "asService"}, {"Container", "up"},
				{"Container", "terminal"}, {"Container", "withDefaultTerminalCmd"}, {"Directory", "terminal"},
			} {
				t.Run(target[0]+"/"+target[1], func(t *testing.T) {
					field := schemaField(schema.Types.Get(target[0]), target[1])
					require.NotNil(t, field)
					arg := schemaArgument(t, field, tc.present)
					require.NotNil(t, arg.DefaultValue)
					require.Equal(t, "false", *arg.DefaultValue)
					for _, arg := range field.Args {
						require.NotEqual(t, tc.absent, arg.Name, "%s.%s", target[0], target[1])
					}
				})
			}
		})
	}
}
