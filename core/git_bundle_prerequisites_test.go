package core

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/dagger/dagger/util/gitutil"
	"github.com/stretchr/testify/require"
)

// The prerequisites' complete history is packed straight from the source's
// objects: exactly their closure, owned by the bundle repository, which then
// accepts a thin bundle on top of them. With a reachability bitmap too.
func TestCopyGitBundlePrerequisites(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		for _, bitmap := range []bool{false, true} {
			t.Run(format+"/bitmap="+strconv.FormatBool(bitmap), func(t *testing.T) {
				testCopyGitBundlePrerequisites(t, format, bitmap)
			})
		}
	}
}

func testCopyGitBundlePrerequisites(t *testing.T, format string, bitmap bool) {
	ctx := context.Background()
	source := t.TempDir()
	gitBundleTestRun(t, source, "init", "--quiet", "--initial-branch=main", "--object-format="+format)
	commit := func(path, contents string) string {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(source, path)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(source, path), []byte(contents), 0o644))
		gitBundleTestRun(t, source, "add", "-A")
		gitBundleTestRun(t, source, "commit", "--quiet", "-m", path)
		return gitBundleTestRun(t, source, "rev-parse", "HEAD")
	}
	commit("base", "base")
	gitBundleTestRun(t, source, "checkout", "--quiet", "-b", "side")
	side := commit("side/file", "side")
	gitBundleTestRun(t, source, "checkout", "--quiet", "main")
	commit("main", "main")
	gitBundleTestRun(t, source, "merge", "--quiet", "--no-edit", "side")
	merged := gitBundleTestRun(t, source, "rev-parse", "HEAD")
	gitBundleTestRun(t, source, "checkout", "--quiet", "--orphan", "unrelated")
	unrelated := commit("unrelated", "unrelated")
	gitBundleTestRun(t, source, "checkout", "--quiet", "main")
	if bitmap {
		gitBundleTestRun(t, source, "repack", "-adb", "--quiet")
	} else {
		gitBundleTestRun(t, source, "gc", "--quiet")
	}
	// Loose objects next to the pack.
	tip := commit("tip", "tip")
	prerequisites := []*gitutil.Ref{{SHA: merged}, {SHA: side}}

	root := t.TempDir()
	gitBundleTestRun(t, root, "init", "--bare", "--quiet", "--object-format="+format)
	method, err := copyGitBundlePrerequisites(ctx, gitutil.NewGitCLI(gitutil.WithDir(source)), root, prerequisites)
	require.NoError(t, err)
	require.Equal(t, "pack", method)
	objects := func(dir string) []string {
		lines := strings.Split(gitBundleTestRun(t, dir, "rev-list", "--objects", "--missing=error", merged, side), "\n")
		slices.Sort(lines)
		return lines
	}
	closure := objects(source)
	require.Equal(t, closure, objects(root))
	require.Contains(t, gitBundleTestRun(t, root, "count-objects", "-v"), "in-pack: "+strconv.Itoa(len(closure))+"\n", "exactly the prerequisites' closure")
	_, err = runGitEnv(ctx, root, "cat-file", "-e", unrelated)
	require.Error(t, err, "unrelated history is not copied")
	_, err = runGitEnv(ctx, root, "cat-file", "-e", tip)
	require.Error(t, err)
	require.NoFileExists(t, filepath.Join(root, "objects", "info", "alternates"))
	refs, err := runGitEnv(ctx, root, "for-each-ref")
	require.NoError(t, err)
	require.Empty(t, refs, "packing needs no temporary refs")

	bundle := filepath.Join(t.TempDir(), "thin.bundle")
	gitBundleTestRun(t, source, "bundle", "create", "--quiet", bundle, "refs/heads/main", "^"+merged)
	require.NoError(t, verifyGitBundleInRepo(ctx, root, bundle))
	require.NoError(t, fetchGitBundleRefs(ctx, root, bundle, []*GitBundleRef{{Name: "refs/heads/main", SHA: tip}}))
}

// A source the walk cannot complete (here, shallow) falls back to fetching,
// leaving no partial pack behind.
func TestCopyGitBundlePrerequisitesFallback(t *testing.T) {
	ctx := context.Background()
	upstream := t.TempDir()
	gitBundleTestRun(t, upstream, "init", "--quiet", "--initial-branch=main")
	for _, name := range []string{"one", "two", "three"} {
		require.NoError(t, os.WriteFile(filepath.Join(upstream, name), []byte(name), 0o644))
		gitBundleTestRun(t, upstream, "add", "-A")
		gitBundleTestRun(t, upstream, "commit", "--quiet", "-m", name)
	}
	shallow := t.TempDir()
	gitBundleTestRun(t, shallow, "clone", "--quiet", "--depth=2", "file://"+upstream, ".")
	head := gitBundleTestRun(t, shallow, "rev-parse", "HEAD")
	root := t.TempDir()
	gitBundleTestRun(t, root, "init", "--bare", "--quiet")
	require.Error(t, packGitBundlePrerequisites(ctx, gitutil.NewGitCLI(gitutil.WithDir(shallow)), root, []*gitutil.Ref{{SHA: head}}))
	// Whatever fetching a shallow source yields, the fallback is what runs.
	method, err := copyGitBundlePrerequisites(ctx, gitutil.NewGitCLI(gitutil.WithDir(shallow)), root, []*gitutil.Ref{{SHA: head}})
	if err == nil {
		require.Equal(t, "fetch", method)
	}
	packs, err := filepath.Glob(filepath.Join(root, "objects", "pack", "tmp_*"))
	require.NoError(t, err)
	require.Empty(t, packs)
}
