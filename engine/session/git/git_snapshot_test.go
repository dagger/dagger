package git

import (
	"context"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCreateWorkspaceSnapshotBundle(t *testing.T) {
	skipIfNoGit(t)
	repo, home := initRepo(t, "main")
	commitFile(t, repo, home, "tracked.txt", "before\n", "initial")
	remote := filepath.Join(t.TempDir(), "remote.git")
	require.NoError(t, os.MkdirAll(remote, 0o755))
	gitCmd(t, home, remote, "init", "-q", "--bare")
	gitCmd(t, home, repo, "remote", "add", "origin", remote)
	gitCmd(t, home, repo, "push", "-q", "-u", "origin", "main")
	const advertisedRemote = "https://example.com/dagger/snapshot-test.git"
	gitCmd(t, home, repo, "remote", "set-url", "origin", advertisedRemote)
	require.NoError(t, os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("after\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("ignored.txt\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "ignored.txt"), []byte("ignored but captured\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "untracked.txt"), []byte("untracked\n"), 0o644))

	headBefore := gitCmd(t, home, repo, "rev-parse", "HEAD")
	indexBefore, err := os.ReadFile(filepath.Join(repo, ".git", "index"))
	require.NoError(t, err)

	meta, bundlePath, cleanup, err := createWorkspaceSnapshotBundle(context.Background(), repo)
	require.NoError(t, err)
	defer cleanup()
	require.NotEmpty(t, meta.CommitSha)
	require.Equal(t, headBefore, meta.BaseSha)
	require.Equal(t, advertisedRemote, meta.RemoteUrl)
	require.Equal(t, "refs/heads/main", meta.BaseRef)
	require.FileExists(t, bundlePath)

	out := t.TempDir()
	gitCmd(t, home, out, "init", "-q")
	gitCmd(t, home, out, "fetch", "-q", remote, headBefore)
	gitCmd(t, home, out, "fetch", "-q", bundlePath, "refs/dagger/workspace")
	gitCmd(t, home, out, "checkout", "-q", "FETCH_HEAD")
	for name, want := range map[string]string{
		"tracked.txt":   "after\n",
		"ignored.txt":   "ignored but captured\n",
		"untracked.txt": "untracked\n",
	} {
		got, err := os.ReadFile(filepath.Join(out, name))
		require.NoError(t, err)
		require.Equal(t, want, string(got))
	}
	require.Equal(t, headBefore, gitCmd(t, home, repo, "rev-parse", "HEAD"))
	indexAfter, err := os.ReadFile(filepath.Join(repo, ".git", "index"))
	require.NoError(t, err)
	require.Equal(t, indexBefore, indexAfter, "snapshot must not mutate the checkout index")
}

func BenchmarkWorkspaceSnapshot(b *testing.B) {
	if _, err := os.Stat("/usr/bin/git"); err != nil {
		b.Skip("git is not installed")
	}
	b.Run("synthetic-64MiB-base", func(b *testing.B) {
		benchmarkWorkspaceSnapshotFixture(b, benchmarkSnapshotRepo(b))
	})
	b.Run("dagger-dagger", func(b *testing.B) {
		benchmarkWorkspaceSnapshotFixture(b, benchmarkDaggerRepo(b))
	})
}

func benchmarkWorkspaceSnapshotFixture(b *testing.B, fixture benchmarkSnapshotFixture) {
	b.Run("before-cold-filesync-including-git", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(fixture.filesyncBytes)
		for range b.N {
			if err := walkAndReadWorkspace(fixture.repo, true); err != nil {
				b.Fatal(err)
			}
		}
		b.ReportMetric(float64(fixture.filesyncBytes), "filesync-bytes")
		b.ReportMetric(float64(fixture.gitBytes), "git-dir-bytes")
	})
	b.Run("full-git-bundle-snapshot", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(fixture.treeBytes)
		var bundleBytes int64
		for range b.N {
			_, bundle, cleanup, err := createWorkspaceSnapshotBundle(context.Background(), fixture.repo)
			if err != nil {
				b.Fatal(err)
			}
			bundleBytes = benchmarkBundleSize(b, bundle)
			cleanup()
		}
		b.ReportMetric(float64(bundleBytes), "bundle-bytes")
	})
	b.Run("thin-git-bundle-over-remote-base", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(fixture.deltaBytes)
		var bundleBytes int64
		for range b.N {
			bundle, cleanup, err := createThinWorkspaceSnapshotBundle(context.Background(), fixture.repo, fixture.baseSHA)
			if err != nil {
				b.Fatal(err)
			}
			bundleBytes = benchmarkBundleSize(b, bundle)
			cleanup()
		}
		b.ReportMetric(float64(bundleBytes), "bundle-bytes")
	})
	b.Run("thin-bundle-warm-engine-end-to-end", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(fixture.deltaBytes)
		var bundleBytes int64
		var packTime, importTime time.Duration
		for range b.N {
			started := time.Now()
			bundle, cleanup, err := createThinWorkspaceSnapshotBundle(context.Background(), fixture.repo, fixture.baseSHA)
			if err != nil {
				b.Fatal(err)
			}
			packTime += time.Since(started)
			bundleBytes = benchmarkBundleSize(b, bundle)

			started = time.Now()
			if err := benchmarkMaterializeThinBundle(context.Background(), fixture.repo, bundle); err != nil {
				cleanup()
				b.Fatal(err)
			}
			importTime += time.Since(started)
			cleanup()
		}
		b.ReportMetric(float64(bundleBytes), "bundle-bytes")
		b.ReportMetric(float64(packTime.Microseconds())/float64(b.N)/1000, "client-pack-ms")
		b.ReportMetric(float64(importTime.Microseconds())/float64(b.N)/1000, "engine-import-ms")
	})
	if fixture.remoteURL != "" && os.Getenv("DAGGER_BENCH_REMOTE_GIT") == "1" {
		b.Run("thin-bundle-cold-engine-end-to-end", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(fixture.deltaBytes)
			var bundleBytes, remoteBytes int64
			var packTime, fetchTime, importTime time.Duration
			for range b.N {
				started := time.Now()
				bundle, cleanup, err := createThinWorkspaceSnapshotBundle(context.Background(), fixture.repo, fixture.baseSHA)
				if err != nil {
					b.Fatal(err)
				}
				packTime += time.Since(started)
				bundleBytes = benchmarkBundleSize(b, bundle)

				remoteSize, fetchedFor, importedFor, err := benchmarkMaterializeThinBundleCold(
					context.Background(), fixture.remoteURL, fixture.baseSHA, bundle,
				)
				cleanup()
				if err != nil {
					b.Fatal(err)
				}
				remoteBytes = remoteSize
				fetchTime += fetchedFor
				importTime += importedFor
			}
			b.ReportMetric(float64(bundleBytes), "bundle-upload-bytes")
			b.ReportMetric(float64(remoteBytes), "remote-base-bytes")
			b.ReportMetric(float64(packTime.Microseconds())/float64(b.N)/1000, "client-pack-ms")
			b.ReportMetric(float64(fetchTime.Microseconds())/float64(b.N)/1000, "engine-fetch-ms")
			b.ReportMetric(float64(importTime.Microseconds())/float64(b.N)/1000, "engine-import-ms")
		})
	}
}

type benchmarkSnapshotFixture struct {
	repo          string
	baseSHA       string
	remoteURL     string
	treeBytes     int64
	filesyncBytes int64
	gitBytes      int64
	deltaBytes    int64
}

// benchmarkSnapshotRepo models an established checkout rather than the
// all-untracked worst case: a 64 MiB committed tree, 16 MiB of tracked edits,
// and 16 MiB of ordinary untracked files.
func benchmarkSnapshotRepo(b *testing.B) benchmarkSnapshotFixture {
	b.Helper()
	repo := b.TempDir()
	cmd := hostGitCommand(context.Background(), repo, nil, "init", "-q", "-b", "main")
	if out, err := cmd.CombinedOutput(); err != nil {
		b.Fatalf("git init: %v: %s", err, out)
	}
	const baseFiles = 4096
	const baseFileSize = 16 << 10
	for i := range baseFiles {
		payload := benchmarkRandomBytes(i, baseFileSize)
		dir := filepath.Join(repo, "d"+strconv.Itoa(i%64))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "f"+strconv.Itoa(i)), payload, 0o644); err != nil {
			b.Fatal(err)
		}
	}
	benchmarkGit(b, repo, "add", ".")
	benchmarkGit(b, repo,
		"-c", "user.name=Dagger Benchmark",
		"-c", "user.email=benchmark@dagger.io",
		"commit", "-q", "-m", "remote base")
	baseSHA := strings.TrimSpace(benchmarkGit(b, repo, "rev-parse", "HEAD"))

	const modifiedFiles = 64
	const modifiedFileSize = 256 << 10
	for i := range modifiedFiles {
		path := filepath.Join(repo, "d"+strconv.Itoa(i%64), "f"+strconv.Itoa(i))
		if err := os.WriteFile(path, benchmarkRandomBytes(10_000+i, modifiedFileSize), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	const untrackedFiles = 128
	const untrackedFileSize = 128 << 10
	for i := range untrackedFiles {
		dir := filepath.Join(repo, "untracked", "d"+strconv.Itoa(i%16))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "f"+strconv.Itoa(i)), benchmarkRandomBytes(20_000+i, untrackedFileSize), 0o644); err != nil {
			b.Fatal(err)
		}
	}

	baseBytes := int64(baseFiles * baseFileSize)
	replacedBaseBytes := int64(modifiedFiles * baseFileSize)
	modifiedBytes := int64(modifiedFiles * modifiedFileSize)
	untrackedBytes := int64(untrackedFiles * untrackedFileSize)
	treeBytes := baseBytes - replacedBaseBytes + modifiedBytes + untrackedBytes
	gitBytes := benchmarkPathBytes(b, filepath.Join(repo, ".git"), false)
	return benchmarkSnapshotFixture{
		repo:          repo,
		baseSHA:       baseSHA,
		treeBytes:     treeBytes,
		filesyncBytes: treeBytes + gitBytes,
		gitBytes:      gitBytes,
		deltaBytes:    modifiedBytes + untrackedBytes,
	}
}

// benchmarkDaggerRepo uses the actual dagger/dagger tree at origin/main as the
// common remote state, then adds the same representative 32 MiB local delta as
// the synthetic fixture. This is deliberately a real plain clone with its own
// object database: the cold filesync baseline must pay for .git, while the
// workspace snapshot transports only the worktree delta over origin/main.
func benchmarkDaggerRepo(b *testing.B) benchmarkSnapshotFixture {
	b.Helper()
	source := strings.TrimSpace(benchmarkGit(b, ".", "rev-parse", "--show-toplevel"))
	baseSHA := strings.TrimSpace(benchmarkGit(b, source, "rev-parse", "origin/main^{commit}"))
	repo := filepath.Join(b.TempDir(), "dagger")
	cmd := hostGitCommand(context.Background(), source, nil, "clone", "-q", "--local", "--no-hardlinks", "--no-checkout", source, repo)
	if out, err := cmd.CombinedOutput(); err != nil {
		b.Fatalf("clone dagger/dagger benchmark fixture: %v: %s", err, out)
	}
	benchmarkGit(b, repo, "checkout", "-q", baseSHA)
	benchmarkGit(b, repo, "remote", "set-url", "origin", "https://github.com/dagger/dagger.git")

	tracked := strings.Split(benchmarkGit(b, repo, "ls-files"), "\n")
	modified := 0
	const modifiedFiles = 64
	const modifiedFileSize = 256 << 10
	for _, rel := range tracked {
		if rel == "" {
			continue
		}
		path := filepath.Join(repo, filepath.FromSlash(rel))
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if err := os.WriteFile(path, benchmarkRandomBytes(30_000+modified, modifiedFileSize), info.Mode().Perm()); err != nil {
			b.Fatal(err)
		}
		modified++
		if modified == modifiedFiles {
			break
		}
	}
	if modified != modifiedFiles {
		b.Fatalf("dagger/dagger fixture has only %d modifiable tracked files", modified)
	}

	const untrackedFiles = 128
	const untrackedFileSize = 128 << 10
	for i := range untrackedFiles {
		dir := filepath.Join(repo, "benchmark-untracked", "d"+strconv.Itoa(i%16))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "f"+strconv.Itoa(i)), benchmarkRandomBytes(40_000+i, untrackedFileSize), 0o644); err != nil {
			b.Fatal(err)
		}
	}

	treeBytes := benchmarkPathBytes(b, repo, true)
	gitBytes := benchmarkPathBytes(b, filepath.Join(repo, ".git"), false)
	return benchmarkSnapshotFixture{
		repo:          repo,
		baseSHA:       baseSHA,
		remoteURL:     "https://github.com/dagger/dagger.git",
		treeBytes:     treeBytes,
		filesyncBytes: treeBytes + gitBytes,
		gitBytes:      gitBytes,
		deltaBytes:    int64(modifiedFiles*modifiedFileSize + untrackedFiles*untrackedFileSize),
	}
}

func benchmarkPathBytes(b *testing.B, root string, skipGit bool) int64 {
	b.Helper()
	var total int64
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if skipGit && entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	if err != nil {
		b.Fatal(err)
	}
	return total
}

func benchmarkRandomBytes(seed, size int) []byte {
	payload := make([]byte, size)
	_, _ = rand.New(rand.NewSource(int64(seed))).Read(payload) //nolint:gosec // deterministic benchmark data
	return payload
}

func benchmarkGit(b *testing.B, repo string, args ...string) string {
	b.Helper()
	cmd := hostGitCommand(context.Background(), repo, nil, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		b.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func benchmarkBundleSize(b *testing.B, path string) int64 {
	b.Helper()
	info, err := os.Stat(path)
	if err != nil {
		b.Fatal(err)
	}
	return info.Size()
}

// createThinWorkspaceSnapshotBundle models the proposed transport: the exact
// remote base is a bundle prerequisite, so the payload contains only the
// synthetic snapshot commit, changed tracked objects, and untracked objects.
func createThinWorkspaceSnapshotBundle(ctx context.Context, checkout, baseSHA string) (string, func(), error) {
	commonDir, err := runHostGit(ctx, checkout, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", func() {}, err
	}
	tmpDir, err := os.MkdirTemp("", "dagger-thin-workspace-snapshot-*")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(tmpDir) }
	gitDir := filepath.Join(tmpDir, "repo.git")
	bundlePath := filepath.Join(tmpDir, "snapshot.bundle")
	if _, err := runSnapshotGit(ctx, checkout, nil, "init", "-q", "--bare", gitDir); err != nil {
		cleanup()
		return "", func() {}, err
	}

	indexPath := filepath.Join(tmpDir, "index")
	checkoutIndex, err := runHostGit(ctx, checkout, "rev-parse", "--path-format=absolute", "--git-path", "index")
	if err != nil {
		cleanup()
		return "", func() {}, err
	}
	index, err := os.ReadFile(strings.TrimSpace(checkoutIndex))
	if err != nil {
		cleanup()
		return "", func() {}, err
	}
	if err := os.WriteFile(indexPath, index, 0o600); err != nil {
		cleanup()
		return "", func() {}, err
	}
	env := []string{
		"GIT_DIR=" + gitDir,
		"GIT_WORK_TREE=" + checkout,
		"GIT_INDEX_FILE=" + indexPath,
		"GIT_OBJECT_DIRECTORY=" + filepath.Join(gitDir, "objects"),
		"GIT_ALTERNATE_OBJECT_DIRECTORIES=" + filepath.Join(strings.TrimSpace(commonDir), "objects"),
		"GIT_AUTHOR_NAME=Dagger Workspace Snapshot",
		"GIT_AUTHOR_EMAIL=snapshot@dagger.io",
		"GIT_COMMITTER_NAME=Dagger Workspace Snapshot",
		"GIT_COMMITTER_EMAIL=snapshot@dagger.io",
		"GIT_AUTHOR_DATE=@0 +0000",
		"GIT_COMMITTER_DATE=@0 +0000",
	}
	if _, err := runSnapshotGit(ctx, checkout, env, "add", "-A", "--", ".", ":(exclude).git"); err != nil {
		cleanup()
		return "", func() {}, err
	}
	tree, err := runSnapshotGit(ctx, checkout, env, "write-tree")
	if err != nil {
		cleanup()
		return "", func() {}, err
	}
	commit, err := runSnapshotGitInput(ctx, checkout, env, strings.NewReader("Dagger thin workspace snapshot\n"), "commit-tree", strings.TrimSpace(tree), "-p", baseSHA)
	if err != nil {
		cleanup()
		return "", func() {}, err
	}
	if _, err := runSnapshotGit(ctx, checkout, env, "update-ref", "refs/dagger/workspace", strings.TrimSpace(commit)); err != nil {
		cleanup()
		return "", func() {}, err
	}
	if _, err := runSnapshotGit(ctx, checkout, env, "bundle", "create", bundlePath, "refs/dagger/workspace", "^"+baseSHA); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return bundlePath, cleanup, nil
}

func walkAndReadWorkspace(root string, includeGit bool) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !includeGit && entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(io.Discard, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}

// benchmarkMaterializeThinBundle models the warm-engine half of the proposed
// transport. The base checkout's object database stands in for an engine Git
// mirror which has already fetched the exact prerequisite. The thin bundle is
// then verified, imported, and checked out into a new worktree.
func benchmarkMaterializeThinBundle(ctx context.Context, baseCheckout, bundlePath string) error {
	commonDir, err := runHostGit(ctx, baseCheckout, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return err
	}
	root, err := os.MkdirTemp("", "dagger-thin-workspace-import-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)

	if _, err := runSnapshotGit(ctx, root, nil, "init", "-q", "--initial-branch=main"); err != nil {
		return err
	}
	env := []string{"GIT_ALTERNATE_OBJECT_DIRECTORIES=" + filepath.Join(strings.TrimSpace(commonDir), "objects")}
	if _, err := runSnapshotGit(ctx, root, env, "fetch", "-q", bundlePath, "+refs/dagger/workspace:refs/dagger/workspace"); err != nil {
		return err
	}
	if _, err := runSnapshotGit(ctx, root, env, "read-tree", "refs/dagger/workspace"); err != nil {
		return err
	}
	if _, err := runSnapshotGit(ctx, root, env, "checkout-index", "-a", "-f"); err != nil {
		return err
	}
	return nil
}

// benchmarkMaterializeThinBundleCold measures the other important deployment
// shape: an engine without the prerequisite in its mirror. It deliberately
// fetches the pinned commit into a new repository before importing the thin
// bundle. The returned byte count is the on-disk object payload received from
// the remote; protocol overhead is not included.
func benchmarkMaterializeThinBundleCold(ctx context.Context, remoteURL, baseSHA, bundlePath string) (int64, time.Duration, time.Duration, error) {
	root, err := os.MkdirTemp("", "dagger-thin-workspace-cold-import-*")
	if err != nil {
		return 0, 0, 0, err
	}
	defer os.RemoveAll(root)
	if _, err := runSnapshotGit(ctx, root, nil, "init", "-q", "--initial-branch=main"); err != nil {
		return 0, 0, 0, err
	}
	if _, err := runSnapshotGit(ctx, root, nil, "remote", "add", "origin", remoteURL); err != nil {
		return 0, 0, 0, err
	}
	fetchStarted := time.Now()
	if _, err := runSnapshotGit(ctx, root, nil, "fetch", "-q", "--depth=1", "--no-tags", "origin", baseSHA); err != nil {
		return 0, 0, 0, err
	}
	fetchTime := time.Since(fetchStarted)
	remoteBytes, err := pathBytes(filepath.Join(root, ".git", "objects"))
	if err != nil {
		return 0, 0, 0, err
	}

	importStarted := time.Now()
	if _, err := runSnapshotGit(ctx, root, nil, "fetch", "-q", bundlePath, "+refs/dagger/workspace:refs/dagger/workspace"); err != nil {
		return 0, 0, 0, err
	}
	if _, err := runSnapshotGit(ctx, root, nil, "read-tree", "refs/dagger/workspace"); err != nil {
		return 0, 0, 0, err
	}
	if _, err := runSnapshotGit(ctx, root, nil, "checkout-index", "-a", "-f"); err != nil {
		return 0, 0, 0, err
	}
	return remoteBytes, fetchTime, time.Since(importStarted), nil
}

func pathBytes(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	return total, err
}
