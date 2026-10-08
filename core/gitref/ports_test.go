package gitref

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Use the existing explicit-selector builder: resolving a known repository and
// subdirectory must not need unauthenticated import-path discovery.
func TestExplicitGitURLPreservesTransportPort(t *testing.T) {
	for _, clone := range []string{
		"http://localhost:8929/team/project.git",
		"https://git.example.org:8443/team/project.git",
		"https://alice@git.example.org:8443/team/project.git",
		"ssh://git@git.example.org:2222/team/project.git",
		"http://localhost/team/project.git",
		"https://git.example.org/team/project.git",
	} {
		t.Run(clone, func(t *testing.T) {
			parsed, err := Parse(t.Context(), GitURLRefString(clone, "ci", "main"))
			require.NoError(t, err)
			require.Equal(t, clone, parsed.CloneRef)
			require.Equal(t, clone, parsed.SourceCloneRef)
			require.Equal(t, "ci", parsed.RepoRootSubdir)
			require.Equal(t, "main", parsed.ModVersion)
			require.Equal(t, GitRefSelector, parsed.Selector)
		})
	}
}
