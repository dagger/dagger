package core

import (
	"context"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (WorkspaceSuite) TestDoctorCLI(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := c.Container().From(alpineImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithEnvVariable("DAGGER_NO_UPDATE_CHECK", "1").
		WithEnvVariable("DAGGER_CLOUD_TOKEN", "").
		WithWorkdir("/work").WithDirectory("/work", c.Directory().WithNewDirectory(".git"))
	for _, tc := range []struct {
		name    string
		config  string
		lock    string
		command []string
		expect  dagger.ReturnType
		want    []string
	}{
		{"missing files", "", "", []string{"workspace", "doctor"}, dagger.ReturnTypeSuccess, []string{"WARN Workspace config", "WARN Lockfile", "PASS Engine", "WARN Cloud login"}},
		{"unloadable module is not checked", "[modules.broken]\nsource = \"does-not-exist\"\n", "[[\"version\",\"2\"]]\n", []string{"workspace", "doctor"}, dagger.ReturnTypeSuccess, []string{"PASS Workspace config", "PASS Lockfile", "PASS Engine"}},
		{"hidden alias", "", "", []string{"doctor"}, dagger.ReturnTypeSuccess, []string{"WARN Workspace config", "PASS Engine"}},
		{"invalid files", "[broken", "broken", []string{"workspace", "doctor"}, dagger.ReturnTypeFailure, []string{"FAIL Workspace config", "FAIL Lockfile", "PASS Engine", "WARN Cloud login"}},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			ctr := base
			if tc.config != "" {
				ctr = ctr.WithNewFile("/work/dagger.toml", tc.config)
			}
			if tc.lock != "" {
				ctr = ctr.WithNewFile("/work/dagger.lock", tc.lock)
			}
			out, err := ctr.WithExec(append([]string{"dagger"}, tc.command...), dagger.ContainerWithExecOpts{
				ExperimentalPrivilegedNesting: true, Expect: tc.expect,
			}).Stdout(ctx)
			require.NoError(t, err)
			for _, want := range tc.want {
				require.Contains(t, out, want)
			}
		})
	}
	t.Run("malformed credentials remain a warning", func(ctx context.Context, t *testctx.T) {
		for _, command := range [][]string{{"workspace", "doctor"}, {"doctor"}} {
			out, err := base.WithEnvVariable("XDG_CONFIG_HOME", "/doctor-config").
				WithNewFile("/doctor-config/dagger/credentials.json", "broken").
				With(workspaceSelectionDaggerExec(command...)).Stdout(ctx)
			require.NoError(t, err)
			require.Contains(t, out, "WARN Cloud login")
			require.Contains(t, out, "PASS Engine")
		}
	})
	t.Run("remote workspace", func(ctx context.Context, t *testctx.T) {
		source := c.Directory().WithNewFile("dagger.toml", "[modules.broken]\nsource = \"does-not-exist\"\n")
		ref := workspaceSelectionRemoteRef(ctx, t, c, source)
		out, err := base.With(workspaceSelectionDaggerExec("-W", ref, "workspace", "doctor")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "PASS Workspace config")
		require.Contains(t, out, "WARN Lockfile")
		require.Contains(t, out, "PASS Engine")
	})
}
