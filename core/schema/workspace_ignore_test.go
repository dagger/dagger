package schema

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/moby/patternmatcher"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceIgnoreForPath(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		ignore []string
		read   string
		want   []string
	}{
		{
			name:   "root keeps configured patterns",
			ignore: []string{".delta", "pkg/vendor", "*.log", "**/.generated"},
			read:   ".",
			want:   []string{".delta", "pkg/vendor", "*.log", "**/.generated"},
		},
		{
			name:   "exact ignored dir excludes everything",
			ignore: []string{".delta"},
			read:   ".delta",
			want:   []string{"*"},
		},
		{
			name:   "negation keeps bang through a covering match",
			ignore: []string{".delta", "!.delta/keep"},
			read:   ".delta",
			want:   []string{"*", "!keep"},
		},
		{
			name:   "negation can unignore the read path",
			ignore: []string{".delta", "!.delta"},
			read:   ".delta",
			want:   []string{"*", "!*"},
		},
		{
			name:   "root glob is not applied under a subdirectory",
			ignore: []string{"*.log"},
			read:   "pkg",
		},
		{
			name:   "starstar tail leftover at matching prefix",
			ignore: []string{"**/foo/bar"},
			read:   "foo",
			want:   []string{"**/foo/bar", "bar"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, workspaceIgnoreForPath(tc.ignore, tc.read))
		})
	}
}

func TestWorkspaceIgnoreRebaseEquivalence(t *testing.T) {
	t.Parallel()
	patterns := []string{
		".delta",
		"pkg/vendor",
		"*.log",
		"*/vendor",
		"pkg/**/tmp",
		"a/**/b/tmp",
		"**/foo/bar",
		"**/.generated",
		"pkg/*/tmp",
		"**/a/**/c",
		"pkg/**/b/**/c",
	}
	reads := []string{".", "pkg", "pkg/foo", "a/x", "a/x/b", "foo", "x/foo", "pkg/vendor", "x/a/y/z", "pkg/b/x/y"}
	candidates := []string{
		".delta/worktrees/nested/go.mod",
		"pkg/.delta/nested/go.mod",
		"pkg/vendor/go.mod",
		"pkg/foo/tmp/x",
		"pkg/foo/bar/tmp/x",
		"a/x/b/tmp",
		"a/x/y/b/tmp",
		"a/x/tmp",
		"foo/bar",
		"x/foo/bar",
		"foo/x/foo/bar",
		"x/foo/y/foo/bar",
		"pkg/foo.log",
		"foo.log",
		"pkg/x/vendor/y",
		".generated/x",
		"pkg/.generated/x",
		"x/a/y/z/c",
		"x/a/c",
		"x/a/y/z/foo/c",
		"pkg/b/x/y/c",
		"pkg/b/c",
		"pkg/b/x/y/foo/c",
	}
	for _, pattern := range patterns {
		for _, read := range reads {
			rebased := workspaceIgnoreForPath([]string{pattern}, read)
			for _, candidate := range candidates {
				rel, ok := workspaceIgnoreRelUnder(read, candidate)
				if !ok {
					continue
				}
				rootHide := workspaceIgnoreHiddenBy([]string{pattern}, candidate)
				rebaseHide := workspaceIgnoreHiddenBy(rebased, rel)
				require.Equal(t, rootHide, rebaseHide, "pattern=%q read=%q path=%q rel=%q rebased=%v", pattern, read, candidate, rel, rebased)
			}
		}
	}
}

func TestWorkspaceIgnoreFromConfigBytes(t *testing.T) {
	t.Parallel()

	t.Run("not found is no patterns", func(t *testing.T) {
		t.Parallel()
		got, err := workspaceIgnoreFromConfigBytes(nil, "dagger.toml", os.ErrNotExist)
		require.NoError(t, err)
		require.Nil(t, got)
	})

	t.Run("context cancel is propagated", func(t *testing.T) {
		t.Parallel()
		got, err := workspaceIgnoreFromConfigBytes(nil, "dagger.toml", context.Canceled)
		require.ErrorIs(t, err, context.Canceled)
		require.Nil(t, got)
	})

	t.Run("parse error is no patterns", func(t *testing.T) {
		t.Parallel()
		got, err := workspaceIgnoreFromConfigBytes([]byte("ignore = [\n"), "dagger.toml", nil)
		require.NoError(t, err)
		require.Nil(t, got)
	})
}

func workspaceIgnoreRelUnder(read, full string) (string, bool) {
	if read == "." {
		return full, true
	}
	if full == read {
		return ".", true
	}
	if strings.HasPrefix(full, read+"/") {
		return strings.TrimPrefix(full, read+"/"), true
	}
	return "", false
}

func workspaceIgnoreHiddenBy(patterns []string, p string) bool {
	if len(patterns) == 0 {
		return false
	}
	pm, err := patternmatcher.New(patterns)
	if err != nil {
		return false
	}
	ok, err := pm.MatchesOrParentMatches(p)
	return err == nil && ok
}
