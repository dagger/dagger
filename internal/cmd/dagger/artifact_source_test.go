package daggercmd

import (
	"testing"

	"dagger.io/dagger/core"
	"github.com/stretchr/testify/require"
)

func TestArtifactSourcePaths(t *testing.T) {
	for _, tc := range []struct {
		name, cwd string
		paths     []string
		want      []core.WorkspacePath
	}{
		{"relative to cwd", "/sdk/go", []string{"src", "../python"}, []core.WorkspacePath{"/sdk/go/src", "/sdk/python"}},
		{"absolute starts at root", "/sdk/go", []string{"/docs/guide.md", "/"}, []core.WorkspacePath{"/docs/guide.md", "/"}},
		{"parent within boundary", "/sdk/go", []string{"../../docs"}, []core.WorkspacePath{"/docs"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := artifactSourcePaths(tc.paths, tc.cwd)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
	_, err := artifactSourcePaths([]string{"../../../outside"}, "/sdk/go")
	require.Error(t, err)
}
