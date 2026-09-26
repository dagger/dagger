package schema

import (
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/stretchr/testify/require"
)

func TestShellNestingCallView(t *testing.T) {
	for _, tc := range []struct {
		name          string
		view          call.View
		setter        string
		settings      []dagql.NamedInput
		overrides     []dagql.NamedInput
		wantShell     bool
		wantExecution bool
	}{
		{name: "current default", wantShell: true, wantExecution: true},
		{name: "current opt-out", settings: []dagql.NamedInput{{Name: "disableDaggerInDagger", Value: dagql.Boolean(true)}}},
		{name: "current override disables", overrides: []dagql.NamedInput{{Name: "disableDaggerInDagger", Value: dagql.Opt(dagql.Boolean(true))}}, wantShell: true},
		{name: "current explicit false re-enables", settings: []dagql.NamedInput{{Name: "disableDaggerInDagger", Value: dagql.Boolean(true)}}, overrides: []dagql.NamedInput{{Name: "disableDaggerInDagger", Value: dagql.Opt(dagql.Boolean(false))}}, wantExecution: true},
		{name: "deprecated setter argument ignored", settings: []dagql.NamedInput{{Name: "experimentalPrivilegedNesting", Value: dagql.Opt(dagql.Boolean(false))}}, wantShell: true, wantExecution: true},
		{name: "deprecated execution argument ignored", overrides: []dagql.NamedInput{{Name: "experimentalPrivilegedNesting", Value: dagql.Opt(dagql.Boolean(false))}}, wantShell: true, wantExecution: true},
		{name: "old setter shares configuration", setter: "withDefaultTerminalCmd", settings: []dagql.NamedInput{{Name: "disableDaggerInDagger", Value: dagql.Boolean(true)}}},
		{name: "legacy default", view: "v1.0.0-beta.14"},
		{name: "legacy opt-in", view: "v1.0.0-beta.14", settings: []dagql.NamedInput{{Name: "experimentalPrivilegedNesting", Value: dagql.Opt(dagql.Boolean(true))}}, wantShell: true, wantExecution: true},
		{name: "legacy explicit false disables", view: "v1.0.0-beta.14", settings: []dagql.NamedInput{{Name: "experimentalPrivilegedNesting", Value: dagql.Opt(dagql.Boolean(true))}}, overrides: []dagql.NamedInput{{Name: "experimentalPrivilegedNesting", Value: dagql.Opt(dagql.Boolean(false))}}, wantShell: true},
		{name: "legacy override enables", view: "v1.0.0-beta.14", overrides: []dagql.NamedInput{{Name: "experimentalPrivilegedNesting", Value: dagql.Opt(dagql.Boolean(true))}}, wantExecution: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A legacy server makes missing child-call views visible: internal
			// calls otherwise use the current view, which enables nesting.
			ctx, dag := newNestingTestServer(t, "v0.21.0")
			setter, arg := tc.setter, "interactive"
			if setter == "" {
				setter = "withShell"
			} else {
				arg = "args"
			}
			var ctr dagql.ObjectResult[*core.Container]
			require.NoError(t, dag.Select(ctx, dag.Root(), &ctr,
				dagql.Selector{Field: "container"},
				dagql.Selector{Field: setter, View: tc.view, Args: append([]dagql.NamedInput{
					{Name: arg, Value: dagql.ArrayInput[dagql.String]{"sh"}},
					{Name: "insecureRootCapabilities", Value: dagql.Opt(dagql.Boolean(true))},
				}, tc.settings...)},
			))
			var shell dagql.ObjectResult[core.Command]
			require.NoError(t, dag.Select(ctx, ctr, &shell, dagql.Selector{Field: "shell", View: tc.view}))
			require.Equal(t, tc.wantShell, shell.Self().PrivilegedNesting)
			require.True(t, shell.Self().InsecureRootCapabilities)

			var executed dagql.ObjectResult[*core.Container]
			require.NoError(t, dag.Select(ctx, ctr, &executed, dagql.Selector{Field: "withRun", View: tc.view, Args: append([]dagql.NamedInput{
				{Name: "command", Value: dagql.String("echo two words")},
				{Name: "insecureRootCapabilities", Value: dagql.Opt(dagql.Boolean(false))},
			}, tc.overrides...)}))
			lazy, ok := executed.Self().Lazy.(*core.ContainerExecLazy)
			require.True(t, ok)
			require.Equal(t, tc.wantExecution, lazy.State.Opts.ExperimentalPrivilegedNesting)
			require.False(t, lazy.State.Opts.InsecureRootCapabilities)
			require.Equal(t, []string{"sh", "-c", "echo two words"}, lazy.State.Opts.Args)
		})
	}
}

func TestShellSchemaVersions(t *testing.T) {
	for _, version := range []string{"v0.21.0", "v1.0.0-beta.14", "v1.0.0-beta.15"} {
		t.Run(version, func(t *testing.T) {
			_, dag := newNestingTestServer(t, call.View(version))
			data, err := getSchemaJSON(nil, nil, dag.View, dag)
			require.NoError(t, err)
			schema := decodeSchemaResponse(t, data).Schema
			if version == "v0.21.0" {
				require.Nil(t, schema.Types.Get("Command"))
				for _, field := range []string{"withShell", "shell", "withRun"} {
					require.Nil(t, schemaField(schema.Types.Get("Container"), field))
				}
				return
			}
			require.NotNil(t, schema.Types.Get("Command"))
			for _, name := range []string{"withShell", "shell", "withRun"} {
				field := schemaField(schema.Types.Get("Container"), name)
				require.NotNil(t, field)
				if name == "shell" {
					continue
				}
				legacy := schemaArgument(t, field, "experimentalPrivilegedNesting")
				require.Equal(t, version == "v1.0.0-beta.15", legacy.IsDeprecated)
				if version == "v1.0.0-beta.14" {
					for _, arg := range field.Args {
						require.NotEqual(t, "disableDaggerInDagger", arg.Name)
					}
				} else if name == "withRun" {
					// Absence inherits the configured shell setting, unlike false.
					require.Nil(t, schemaArgument(t, field, "disableDaggerInDagger").DefaultValue)
				}
			}
		})
	}
}
