package core

import (
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

func TestGitModuleSourceSymbolic(t *testing.T) {
	testCases := []struct {
		name        string
		cloneRef    string
		rootSubpath string
		expected    string
	}{
		{
			name:        "Go-style URL",
			cloneRef:    "https://github.com/user/repo.git",
			rootSubpath: "subdir",
			expected:    "https://github.com/user/repo.git/subdir",
		},
		{
			name:        "SCP-like reference",
			cloneRef:    "git@github.com:user/repo.git",
			rootSubpath: "subdir",
			expected:    "git@github.com:user/repo.git/subdir",
		},
		{
			name:        "SCP-like reference with no subdir",
			cloneRef:    "git@github.com:user/repo.git",
			rootSubpath: "",
			expected:    "git@github.com:user/repo.git",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			src := &ModuleSource{
				Kind: ModuleSourceKindGit,
				Git: &GitModuleSource{
					CloneRef: tc.cloneRef,
				},
				SourceRootSubpath: tc.rootSubpath,
			}
			result := src.AsString()
			require.Equal(t, tc.expected, result, "AsString() returned unexpected result")
		})
	}
}

func TestBuiltinModuleSourceClone(t *testing.T) {
	src := &ModuleSource{
		Kind: ModuleSourceKindBuiltin,
		Builtin: &BuiltinModuleSource{
			Name:              "python",
			Description:       "Python runtime",
			ManifestDigest:    digest.FromString("python-runtime"),
			SourceRootSubpath: "runtime",
		},
	}

	clone := src.Clone()
	require.NotSame(t, src.Builtin, clone.Builtin)
	require.Equal(t, src.Builtin, clone.Builtin)

	clone.Builtin.Name = "typescript"
	require.Equal(t, "python", src.Builtin.Name)
}

func TestBuiltinModuleSourceAsString(t *testing.T) {
	src := &ModuleSource{
		Kind: ModuleSourceKindBuiltin,
		Builtin: &BuiltinModuleSource{
			Name: "python",
		},
	}

	require.Equal(t, "builtin:python", src.AsString())
}

func TestBuiltinModuleSourcePin(t *testing.T) {
	dgst := digest.FromString("python-runtime")
	src := &ModuleSource{
		Kind: ModuleSourceKindBuiltin,
		Builtin: &BuiltinModuleSource{
			Name:           "python-runtime",
			ManifestDigest: dgst,
		},
	}

	require.Equal(t, dgst.String(), src.Pin())
}

func TestBuiltinModuleResultCallIdentity(t *testing.T) {
	dgst := digest.FromString("python-runtime")
	src := &ModuleSource{
		Kind: ModuleSourceKindBuiltin,
		Builtin: &BuiltinModuleSource{
			Name:           "python-runtime",
			ManifestDigest: dgst,
		},
	}

	ref, pin, err := resultCallModuleRefAndPin(src)
	require.NoError(t, err)
	require.Equal(t, "builtin:python-runtime", ref)
	require.Equal(t, dgst.String(), pin)
}
