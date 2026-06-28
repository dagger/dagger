package schema

import (
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/stretchr/testify/require"
)

func TestHostPortModuleAllowed(t *testing.T) {
	t.Parallel()

	localSrc := &core.ModuleSource{
		Kind: core.ModuleSourceKindLocal,
	}
	dirSrc := &core.ModuleSource{
		Kind: core.ModuleSourceKindDir,
	}
	gitSrc := &core.ModuleSource{
		Kind: core.ModuleSourceKindGit,
		Git: &core.GitModuleSource{
			Symbolic: "github.com/acme/trusted/subdir",
		},
	}

	for _, tc := range []struct {
		name    string
		allowed []string
		src     *core.ModuleSource
		want    bool
	}{
		{
			name:    "local allowed",
			allowed: []string{"local"},
			src:     localSrc,
			want:    true,
		},
		{
			name:    "directory source allowed as local",
			allowed: []string{"local"},
			src:     dirSrc,
			want:    true,
		},
		{
			name:    "local denied without local or all",
			allowed: []string{"github.com/acme/trusted/subdir"},
			src:     localSrc,
			want:    false,
		},
		{
			name:    "git allowed by exact symbolic ref",
			allowed: []string{"github.com/acme/trusted/subdir"},
			src:     gitSrc,
			want:    true,
		},
		{
			name:    "git denied by different ref",
			allowed: []string{"github.com/acme/other"},
			src:     gitSrc,
			want:    false,
		},
		{
			name:    "git denied by local",
			allowed: []string{"local"},
			src:     gitSrc,
			want:    false,
		},
		{
			name:    "all allows local",
			allowed: []string{"all"},
			src:     localSrc,
			want:    true,
		},
		{
			name:    "all allows git",
			allowed: []string{"all"},
			src:     gitSrc,
			want:    true,
		},
		{
			name:    "trim spaces",
			allowed: []string{" github.com/acme/trusted/subdir "},
			src:     gitSrc,
			want:    true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, hostPortModuleAllowed(tc.allowed, tc.src))
		})
	}
}
