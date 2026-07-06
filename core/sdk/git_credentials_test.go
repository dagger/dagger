package sdk

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGitCredentialHostsFromManifests(t *testing.T) {
	t.Parallel()

	pyproject := `
[project]
dependencies = [
    "coolpy @ git+https://gitlab.com/org/private.git@v1#subdirectory=coolpy",
    "requests>=2.0",
]

# the form 'uv add' actually writes
[tool.uv.sources]
mylib = { git = "https://git.corp.example.com/acme/mylib", tag = "v1" }
`
	packageJSON := `{
  "dependencies": {
    "cooljs": "git+https://user@github.com/org/private-js.git#main",
    "left-pad": "^1.3.0",
    "ssh-dep": "git+ssh://git@github.com/org/ignored.git"
  }
}`
	uvLock := `
[[package]]
name = "translock"
source = { git = "https://gitlab.example.com/acme/translock?tag=v2#abc123" }
`

	require.Equal(t,
		[]string{"git.corp.example.com", "github.com", "gitlab.com"},
		gitCredentialHostsFromManifests(pyproject, packageJSON),
	)
	// transitive git deps recorded only in lockfiles are allowlisted too
	require.Equal(t,
		[]string{"gitlab.example.com"},
		gitCredentialHostsFromManifests(uvLock),
	)
	require.Empty(t, gitCredentialHostsFromManifests(`{"dependencies": {"left-pad": "^1.3.0"}}`))
}
