package daggercmd

import (
	"bytes"
	"testing"

	"github.com/dagger/dagger/core/workspace"
	"github.com/stretchr/testify/require"
)

func TestInstalledModuleVersion(t *testing.T) {
	for _, tc := range []struct{ source, version, err string }{
		{"github.com/acme/repo@v1", "v1", ""},
		{"github.com/acme/repo@feature@backup", "feature@backup", ""},
		{"https://github.com/acme/repo.git#main:tools", "main", ""},
		{"git@github.com:acme/repo.git@v2", "v2", ""},
		{"./local", "", "has a local source"},
		{"github.com/acme/repo", "", "has no explicit version request"},
	} {
		version, err := installedModuleVersion(workspace.ModuleSelection{Name: "tools", Entry: workspace.ModuleEntry{Source: tc.source, Pin: "abcdef"}})
		if tc.err != "" {
			require.ErrorContains(t, err, tc.err)
		} else {
			require.NoError(t, err)
		}
		require.Equal(t, tc.version, version)
	}
	var out bytes.Buffer
	require.NoError(t, writeModuleSourceMatch(&out, workspace.ModuleSelection{Name: "tools", Source: "github.com/acme/repo"}))
	require.Equal(t, "Matched installed module \"tools\" by source \"github.com/acme/repo\".\n", out.String())
}

func TestModuleVersionCommands(t *testing.T) {
	for _, args := range [][]string{{"module", "update"}, {"update"}} {
		cmd, _, err := rootCmd.Find(args)
		require.NoError(t, err)
		require.NotNil(t, cmd.Flags().Lookup("version"))
	}
	cmd, _, err := rootCmd.Find([]string{"mod", "version"})
	require.NoError(t, err)
	require.Same(t, moduleVersionCmd, cmd)
}
