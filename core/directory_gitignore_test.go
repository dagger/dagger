package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPartitionIgnoredAdditions(t *testing.T) {
	t.Run("outermost ignored directories cover their contents", func(t *testing.T) {
		added := []string{
			"node_modules/",
			"node_modules/dep/",
			"node_modules/dep/index.js",
			"node_modules/dep/node_modules/",
			"node_modules/dep/node_modules/sub/",
			"node_modules/dep/node_modules/sub/index.js",
			"src/app.js",
		}
		ignored := []string{
			"node_modules/",
			"node_modules/dep/",
			"node_modules/dep/index.js",
			"node_modules/dep/node_modules/",
			"node_modules/dep/node_modules/sub/",
			"node_modules/dep/node_modules/sub/index.js",
		}
		dirs, files, parents := partitionIgnoredAdditions(added, ignored)
		require.Equal(t, []string{"node_modules"}, dirs)
		require.Empty(t, files)
		require.Empty(t, parents)
	})

	t.Run("ignored files inside new directories prune deepest first", func(t *testing.T) {
		added := []string{
			"build/",
			"build/out/",
			"build/out/a.o",
			"build/out/b.o",
			"src/",
			"src/main.c",
			"src/main.o",
		}
		ignored := []string{
			"build/out/a.o",
			"build/out/b.o",
			"src/main.o",
		}
		dirs, files, parents := partitionIgnoredAdditions(added, ignored)
		require.Empty(t, dirs)
		require.Equal(t, []string{"build/out/a.o", "build/out/b.o", "src/main.o"}, files)
		// build/out (depth 1) sorts before build and src (depth 0); a directory
		// is only a candidate when it was itself added.
		require.Equal(t, []string{"build/out", "build", "src"}, parents)
	})

	t.Run("pre-existing parents are not pruning candidates", func(t *testing.T) {
		added := []string{"src/main.o"}
		ignored := []string{"src/main.o"}
		dirs, files, parents := partitionIgnoredAdditions(added, ignored)
		require.Empty(t, dirs)
		require.Equal(t, []string{"src/main.o"}, files)
		require.Empty(t, parents)
	})

	t.Run("files under an ignored directory are not listed twice", func(t *testing.T) {
		added := []string{".venv/", ".venv/bin/", ".venv/bin/python", "cache.db"}
		ignored := []string{".venv/bin/python", ".venv/", "cache.db"}
		dirs, files, parents := partitionIgnoredAdditions(added, ignored)
		require.Equal(t, []string{".venv"}, dirs)
		require.Equal(t, []string{"cache.db"}, files)
		require.Empty(t, parents)
	})
}
