package git

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/containerd/containerd/content/local"
	"github.com/containerd/containerd/diff/apply"
	"github.com/containerd/containerd/diff/walking"
	ctdmetadata "github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/snapshots"
	"github.com/containerd/containerd/snapshots/native"
	"github.com/moby/buildkit/cache"
	"github.com/moby/buildkit/cache/metadata"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/snapshot"
	containerdsnapshot "github.com/moby/buildkit/snapshot/containerd"
	"github.com/moby/buildkit/source"
	"github.com/moby/buildkit/util/gitutil"
	"github.com/moby/buildkit/util/leaseutil"
	"github.com/moby/buildkit/util/progress"
	"github.com/moby/buildkit/util/progress/logs"
	"github.com/moby/buildkit/util/winlayers"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	bolt "go.etcd.io/bbolt"
)

func TestRepeatedFetch(t *testing.T) {
	testRepeatedFetch(t, false)
}
func TestRepeatedFetchKeepGitDir(t *testing.T) {
	testRepeatedFetch(t, true)
}

func testRepeatedFetch(t *testing.T, keepGitDir bool) {
	if runtime.GOOS == "windows" {
		t.Skip("Depends on unimplemented containerd bind-mount support on Windows")
	}

	t.Parallel()
	ctx := logProgressStreams(context.Background(), t)

	gs := setupGitSource(t, t.TempDir())

	repo := setupGitRepo(t)

	id := &GitIdentifier{Remote: repo.mainURL, KeepGitDir: keepGitDir}

	g, err := gs.Resolve(ctx, id, nil, nil)
	require.NoError(t, err)

	key1, pin1, _, done, err := g.CacheKey(ctx, nil, 0)
	require.NoError(t, err)
	require.True(t, done)

	expLen := 40
	if keepGitDir {
		expLen += 4
	}

	require.Equal(t, expLen, len(key1))
	require.Equal(t, 40, len(pin1))

	ref1, err := g.Snapshot(ctx, nil)
	require.NoError(t, err)
	defer ref1.Release(context.TODO())

	mount, err := ref1.Mount(ctx, true, nil)
	require.NoError(t, err)

	lm := snapshot.LocalMounter(mount)
	dir, err := lm.Mount()
	require.NoError(t, err)
	defer lm.Unmount()

	dt, err := os.ReadFile(filepath.Join(dir, "def"))
	require.NoError(t, err)

	require.Equal(t, "bar\n", string(dt))

	_, err = os.Lstat(filepath.Join(dir, "ghi"))
	require.ErrorAs(t, err, &os.ErrNotExist)

	_, err = os.Lstat(filepath.Join(dir, "sub/subfile"))
	require.ErrorAs(t, err, &os.ErrNotExist)

	// second fetch returns same dir
	id = &GitIdentifier{Remote: repo.mainURL, Ref: "master", KeepGitDir: keepGitDir}

	g, err = gs.Resolve(ctx, id, nil, nil)
	require.NoError(t, err)

	key2, pin2, _, _, err := g.CacheKey(ctx, nil, 0)
	require.NoError(t, err)

	require.Equal(t, key1, key2)
	require.Equal(t, pin1, pin2)

	ref2, err := g.Snapshot(ctx, nil)
	require.NoError(t, err)
	defer ref2.Release(context.TODO())

	require.Equal(t, ref1.ID(), ref2.ID())

	id = &GitIdentifier{Remote: repo.mainURL, Ref: "feature", KeepGitDir: keepGitDir}

	g, err = gs.Resolve(ctx, id, nil, nil)
	require.NoError(t, err)

	key3, pin3, _, _, err := g.CacheKey(ctx, nil, 0)
	require.NoError(t, err)
	require.NotEqual(t, key1, key3)
	require.NotEqual(t, pin1, pin3)

	ref3, err := g.Snapshot(ctx, nil)
	require.NoError(t, err)
	defer ref3.Release(context.TODO())

	mount, err = ref3.Mount(ctx, true, nil)
	require.NoError(t, err)

	lm = snapshot.LocalMounter(mount)
	dir, err = lm.Mount()
	require.NoError(t, err)
	defer lm.Unmount()

	dt, err = os.ReadFile(filepath.Join(dir, "ghi"))
	require.NoError(t, err)

	require.Equal(t, "baz\n", string(dt))

	dt, err = os.ReadFile(filepath.Join(dir, "sub/subfile"))
	require.NoError(t, err)

	require.Equal(t, "subcontents\n", string(dt))
}

func TestFetchBySHA(t *testing.T) {
	testFetchBySHA(t, false)
}
func TestFetchBySHAKeepGitDir(t *testing.T) {
	testFetchBySHA(t, true)
}

func testFetchBySHA(t *testing.T, keepGitDir bool) {
	if runtime.GOOS == "windows" {
		t.Skip("Depends on unimplemented containerd bind-mount support on Windows")
	}

	t.Parallel()
	ctx := namespaces.WithNamespace(context.Background(), "buildkit-test")
	ctx = logProgressStreams(ctx, t)

	gs := setupGitSource(t, t.TempDir())

	repo := setupGitRepo(t)

	cmd := exec.Command("git", "rev-parse", "feature")
	cmd.Dir = repo.mainPath

	out, err := cmd.Output()
	require.NoError(t, err)

	sha := strings.TrimSpace(string(out))
	require.Equal(t, 40, len(sha))

	id := &GitIdentifier{Remote: repo.mainURL, Ref: sha, KeepGitDir: keepGitDir}

	g, err := gs.Resolve(ctx, id, nil, nil)
	require.NoError(t, err)

	key1, pin1, _, done, err := g.CacheKey(ctx, nil, 0)
	require.NoError(t, err)
	require.True(t, done)

	expLen := 40
	if keepGitDir {
		expLen += 4
	}

	require.Equal(t, expLen, len(key1))
	require.Equal(t, 40, len(pin1))

	ref1, err := g.Snapshot(ctx, nil)
	require.NoError(t, err)
	defer ref1.Release(context.TODO())

	mount, err := ref1.Mount(ctx, true, nil)
	require.NoError(t, err)

	lm := snapshot.LocalMounter(mount)
	dir, err := lm.Mount()
	require.NoError(t, err)
	defer lm.Unmount()

	dt, err := os.ReadFile(filepath.Join(dir, "ghi"))
	require.NoError(t, err)

	require.Equal(t, "baz\n", string(dt))

	dt, err = os.ReadFile(filepath.Join(dir, "sub/subfile"))
	require.NoError(t, err)

	require.Equal(t, "subcontents\n", string(dt))
}

func TestFetchByTag(t *testing.T) {
	testFetchByTag(t, "lightweight-tag", "third", false, true, false)
}

func TestFetchByTagKeepGitDir(t *testing.T) {
	testFetchByTag(t, "lightweight-tag", "third", false, true, true)
}

func TestFetchByTagFull(t *testing.T) {
	testFetchByTag(t, "refs/tags/lightweight-tag", "third", false, true, true)
}

func TestFetchByAnnotatedTag(t *testing.T) {
	testFetchByTag(t, "v1.2.3", "second", true, false, false)
}

func TestFetchByAnnotatedTagKeepGitDir(t *testing.T) {
	testFetchByTag(t, "v1.2.3", "second", true, false, true)
}

func TestFetchByAnnotatedTagFull(t *testing.T) {
	testFetchByTag(t, "refs/tags/v1.2.3", "second", true, false, true)
}

func TestFetchByBranch(t *testing.T) {
	testFetchByTag(t, "feature", "withsub", false, true, false)
}

func TestFetchByBranchKeepGitDir(t *testing.T) {
	testFetchByTag(t, "feature", "withsub", false, true, true)
}

func TestFetchByBranchFull(t *testing.T) {
	testFetchByTag(t, "refs/heads/feature", "withsub", false, true, true)
}

func TestFetchByRef(t *testing.T) {
	testFetchByTag(t, "test", "feature", false, true, false)
}

func TestFetchByRefKeepGitDir(t *testing.T) {
	testFetchByTag(t, "test", "feature", false, true, true)
}

func TestFetchByRefFull(t *testing.T) {
	testFetchByTag(t, "refs/test", "feature", false, true, true)
}

func testFetchByTag(t *testing.T, tag, expectedCommitSubject string, isAnnotatedTag, hasFoo13File, keepGitDir bool) {
	if runtime.GOOS == "windows" {
		t.Skip("Depends on unimplemented containerd bind-mount support on Windows")
	}

	t.Parallel()
	ctx := namespaces.WithNamespace(context.Background(), "buildkit-test")
	ctx = logProgressStreams(ctx, t)

	gs := setupGitSource(t, t.TempDir())

	repo := setupGitRepo(t)

	id := &GitIdentifier{Remote: repo.mainURL, Ref: tag, KeepGitDir: keepGitDir}

	g, err := gs.Resolve(ctx, id, nil, nil)
	require.NoError(t, err)

	key1, pin1, _, done, err := g.CacheKey(ctx, nil, 0)
	require.NoError(t, err)
	require.True(t, done)

	expLen := 40
	if keepGitDir {
		expLen += 4
	}

	require.Equal(t, expLen, len(key1))
	require.Equal(t, 40, len(pin1))

	ref1, err := g.Snapshot(ctx, nil)
	require.NoError(t, err)
	defer ref1.Release(context.TODO())

	mount, err := ref1.Mount(ctx, true, nil)
	require.NoError(t, err)

	lm := snapshot.LocalMounter(mount)
	dir, err := lm.Mount()
	require.NoError(t, err)
	defer lm.Unmount()

	dt, err := os.ReadFile(filepath.Join(dir, "def"))
	require.NoError(t, err)
	require.Equal(t, "bar\n", string(dt))

	dt, err = os.ReadFile(filepath.Join(dir, "foo13"))
	if hasFoo13File {
		require.Nil(t, err)
		require.Equal(t, "sbb\n", string(dt))
	} else {
		require.ErrorAs(t, err, &os.ErrNotExist)
	}

	if keepGitDir {
		git := gitutil.NewGitCLI(
			gitutil.WithExec(runWithStandardUmask),
			gitutil.WithStreams(func(ctx context.Context) (stdout, stderr io.WriteCloser, flush func()) {
				return logs.NewLogStreams(ctx, false)
			}),
			gitutil.WithWorkTree(dir),
		)

		// get current commit sha
		headCommit, err := git.Run(ctx, "rev-parse", "HEAD")
		require.NoError(t, err)

		// ensure that we checked out the same commit as was in the cache key
		require.Equal(t, strings.TrimSpace(string(headCommit)), pin1)

		if isAnnotatedTag {
			// get commit sha that the annotated tag points to
			annotatedTagCommit, err := git.Run(ctx, "rev-list", "-n", "1", tag)
			require.NoError(t, err)

			// HEAD should match the actual commit sha (and not the sha of the annotated tag,
			// since it's not possible to checkout a non-commit object)
			require.Equal(t, string(annotatedTagCommit), string(headCommit))
		}

		// test that we checked out the correct commit
		// (in the case of an annotated tag, this message is of the commit the annotated tag points to
		// and not the message of the tag)
		gitLogOutput, err := git.Run(ctx, "log", "-n", "1", "--format=%s")
		require.NoError(t, err)
		require.Contains(t, strings.TrimSpace(string(gitLogOutput)), expectedCommitSubject)
	}
}

func TestMultipleRepos(t *testing.T) {
	testMultipleRepos(t, false)
}

func TestMultipleReposKeepGitDir(t *testing.T) {
	testMultipleRepos(t, true)
}

func testMultipleRepos(t *testing.T, keepGitDir bool) {
	if runtime.GOOS == "windows" {
		t.Skip("Depends on unimplemented containerd bind-mount support on Windows")
	}

	t.Parallel()
	ctx := namespaces.WithNamespace(context.Background(), "buildkit-test")
	ctx = logProgressStreams(ctx, t)

	gs := setupGitSource(t, t.TempDir())

	repo := setupGitRepo(t)

	repodir2 := t.TempDir()

	runShell(t, repodir2,
		"git -c init.defaultBranch=master init",
		"git config --local user.email test",
		"git config --local user.name test",
		"echo xyz > xyz",
		"git add xyz",
		"git commit -m initial",
	)
	repoURL2 := serveGitRepo(t, repodir2)

	id := &GitIdentifier{Remote: repo.mainURL, KeepGitDir: keepGitDir}
	id2 := &GitIdentifier{Remote: repoURL2, KeepGitDir: keepGitDir}

	g, err := gs.Resolve(ctx, id, nil, nil)
	require.NoError(t, err)

	g2, err := gs.Resolve(ctx, id2, nil, nil)
	require.NoError(t, err)

	expLen := 40
	if keepGitDir {
		expLen += 4
	}

	key1, pin1, _, _, err := g.CacheKey(ctx, nil, 0)
	require.NoError(t, err)
	require.Equal(t, expLen, len(key1))
	require.Equal(t, 40, len(pin1))

	key2, pin2, _, _, err := g2.CacheKey(ctx, nil, 0)
	require.NoError(t, err)
	require.Equal(t, expLen, len(key2))
	require.Equal(t, 40, len(pin2))

	require.NotEqual(t, key1, key2)
	require.NotEqual(t, pin1, pin2)

	ref1, err := g.Snapshot(ctx, nil)
	require.NoError(t, err)
	defer ref1.Release(context.TODO())

	mount, err := ref1.Mount(ctx, true, nil)
	require.NoError(t, err)

	lm := snapshot.LocalMounter(mount)
	dir, err := lm.Mount()
	require.NoError(t, err)
	defer lm.Unmount()

	ref2, err := g2.Snapshot(ctx, nil)
	require.NoError(t, err)
	defer ref2.Release(context.TODO())

	mount, err = ref2.Mount(ctx, true, nil)
	require.NoError(t, err)

	lm = snapshot.LocalMounter(mount)
	dir2, err := lm.Mount()
	require.NoError(t, err)
	defer lm.Unmount()

	dt, err := os.ReadFile(filepath.Join(dir, "def"))
	require.NoError(t, err)

	require.Equal(t, "bar\n", string(dt))

	dt, err = os.ReadFile(filepath.Join(dir2, "xyz"))
	require.NoError(t, err)

	require.Equal(t, "xyz\n", string(dt))
}

func TestCredentialRedaction(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Depends on unimplemented containerd bind-mount support on Windows")
	}

	t.Parallel()
	ctx := namespaces.WithNamespace(context.Background(), "buildkit-test")
	ctx = logProgressStreams(ctx, t)

	gs := setupGitSource(t, t.TempDir())

	url := "https://user:keepthissecret@non-existant-host/user/private-repo.git"
	id := &GitIdentifier{Remote: url}

	g, err := gs.Resolve(ctx, id, nil, nil)
	require.NoError(t, err)

	_, _, _, _, err = g.CacheKey(ctx, nil, 0)
	require.Error(t, err)
	require.False(t, strings.Contains(err.Error(), "keepthissecret"))
}

func TestSubdir(t *testing.T) {
	testSubdir(t, false)
}
func TestSubdirKeepGitDir(t *testing.T) {
	testSubdir(t, true)
}

func testSubdir(t *testing.T, keepGitDir bool) {
	if runtime.GOOS == "windows" {
		t.Skip("Depends on unimplemented containerd bind-mount support on Windows")
	}

	t.Parallel()

	ctx := logProgressStreams(context.Background(), t)

	gs := setupGitSource(t, t.TempDir())

	repodir := t.TempDir()

	runShell(t, repodir,
		"git -c init.defaultBranch=master init",
		"git config --local user.email test",
		"git config --local user.name test",
		"echo foo > abc",
		"mkdir sub",
		"echo abc > sub/bar",
		"git add abc sub",
		"git commit -m initial",
	)

	repoURL := serveGitRepo(t, repodir)
	id := &GitIdentifier{Remote: repoURL, KeepGitDir: keepGitDir, Subdir: "sub"}

	g, err := gs.Resolve(ctx, id, nil, nil)
	require.NoError(t, err)

	key1, pin1, _, done, err := g.CacheKey(ctx, nil, 0)
	require.NoError(t, err)
	require.True(t, done)

	expLen := 44
	if keepGitDir {
		expLen += 4
	}

	require.Equal(t, expLen, len(key1))
	require.Equal(t, 40, len(pin1))

	ref1, err := g.Snapshot(ctx, nil)
	require.NoError(t, err)
	defer ref1.Release(context.TODO())

	mount, err := ref1.Mount(ctx, true, nil)
	require.NoError(t, err)

	lm := snapshot.LocalMounter(mount)
	dir, err := lm.Mount()
	require.NoError(t, err)
	defer lm.Unmount()

	fis, err := os.ReadDir(dir)
	require.NoError(t, err)

	require.Equal(t, 1, len(fis))

	dt, err := os.ReadFile(filepath.Join(dir, "bar"))
	require.NoError(t, err)

	require.Equal(t, "abc\n", string(dt))
}

func setupGitSource(t *testing.T, tmpdir string) source.Source {
	snapshotter, err := native.NewSnapshotter(filepath.Join(tmpdir, "snapshots"))
	assert.NoError(t, err)

	store, err := local.NewStore(tmpdir)
	require.NoError(t, err)

	db, err := bolt.Open(filepath.Join(tmpdir, "containerdmeta.db"), 0644, nil)
	require.NoError(t, err)

	mdb := ctdmetadata.NewDB(db, store, map[string]snapshots.Snapshotter{
		"native": snapshotter,
	})

	md, err := metadata.NewStore(filepath.Join(tmpdir, "metadata.db"))
	require.NoError(t, err)
	lm := leaseutil.WithNamespace(ctdmetadata.NewLeaseManager(mdb), "buildkit")
	c := mdb.ContentStore()
	applier := winlayers.NewFileSystemApplierWithWindows(c, apply.NewFileSystemApplier(c))
	differ := winlayers.NewWalkingDiffWithWindows(c, walking.NewWalkingDiff(c))

	cm, err := cache.NewManager(cache.ManagerOpt{
		Snapshotter:    snapshot.FromContainerdSnapshotter("native", containerdsnapshot.NSSnapshotter("buildkit", mdb.Snapshotter("native")), nil),
		MetadataStore:  md,
		LeaseManager:   lm,
		ContentStore:   c,
		Applier:        applier,
		Differ:         differ,
		GarbageCollect: mdb.GarbageCollect,
		MountPoolRoot:  filepath.Join(tmpdir, "cachemounts"),
	})
	require.NoError(t, err)

	gs, err := NewSource(Opt{
		CacheAccessor: cm,
	})
	require.NoError(t, err)

	return gs
}

type gitRepoFixture struct {
	mainPath, subPath string // Filesystem paths to the respective repos
	mainURL, subURL   string // HTTP URLs for the respective repos
}

func setupGitRepo(t *testing.T) gitRepoFixture {
	t.Helper()
	dir := t.TempDir()
	srv := serveGitRepo(t, dir)
	fixture := gitRepoFixture{
		subPath:  filepath.Join(dir, "sub"),
		subURL:   srv + "/sub",
		mainPath: filepath.Join(dir, "main"),
		mainURL:  srv + "/main",
	}
	require.NoError(t, os.MkdirAll(fixture.subPath, 0700))
	require.NoError(t, os.MkdirAll(fixture.mainPath, 0700))

	runShell(t, fixture.subPath,
		"git -c init.defaultBranch=master init",
		"git config --local user.email test",
		"git config --local user.name test",
		"echo subcontents > subfile",
		"git add subfile",
		"git commit -m initial",
	)
	runShell(t, fixture.mainPath,
		"git -c init.defaultBranch=master init",
		"git config --local user.email test",
		"git config --local user.name test",
		"echo foo > abc",
		"git add abc",
		"git commit -m initial",
		"git tag --no-sign a/v1.2.3",
		"echo bar > def",
		"git add def",
		"git commit -m second",
		"git tag -a -m \"this is an annotated tag\" v1.2.3",
		"echo sbb > foo13",
		"git add foo13",
		"git commit -m third",
		"git tag --no-sign lightweight-tag",
		"git checkout -B feature",
		"echo baz > ghi",
		"git add ghi",
		"git commit -m feature",
		"git update-ref refs/test $(git rev-parse HEAD)",
		"git submodule add "+fixture.subURL+" sub",
		"git add -A",
		"git commit -m withsub",
		"git checkout master",
	)
	return fixture
}

func serveGitRepo(t *testing.T, root string) string {
	t.Helper()
	gitpath, err := exec.LookPath("git")
	require.NoError(t, err)
	gitversion, _ := exec.Command(gitpath, "version").CombinedOutput()
	t.Logf("%s", gitversion) // E.g. "git version 2.30.2"

	// Serve all repositories under root using the Smart HTTP protocol so
	// they can be cloned as we explicitly disable the file protocol.
	// (Another option would be to use `git daemon` and the Git protocol,
	// but that listens on a fixed port number which is a recipe for
	// disaster in CI. Funnily enough, `git daemon --port=0` works but there
	// is no easy way to discover which port got picked!)

	githttp := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var logs bytes.Buffer
		(&cgi.Handler{
			Path: gitpath,
			Args: []string{"http-backend"},
			Dir:  root,
			Env: []string{
				"GIT_PROJECT_ROOT=" + root,
				"GIT_HTTP_EXPORT_ALL=1",
			},
			Stderr: &logs,
		}).ServeHTTP(w, r)
		if logs.Len() == 0 {
			return
		}
		for {
			line, err := logs.ReadString('\n')
			t.Log("git-http-backend: " + line)
			if err != nil {
				break
			}
		}
	})
	server := httptest.NewServer(&githttp)
	t.Cleanup(server.Close)
	return server.URL
}

func runShell(t *testing.T, dir string, cmds ...string) {
	t.Helper()
	for _, args := range cmds {
		var cmd *exec.Cmd
		if runtime.GOOS == "windows" {
			cmd = exec.Command("powershell", "-command", args)
		} else {
			cmd = exec.Command("sh", "-c", args)
		}
		cmd.Dir = dir
		cmd.Stderr = os.Stderr
		require.NoErrorf(t, cmd.Run(), "error running %v", args)
	}
}

func logProgressStreams(ctx context.Context, t *testing.T) context.Context {
	pr, ctx, cancel := progress.NewContext(ctx)
	done := make(chan struct{})
	t.Cleanup(func() {
		cancel(errors.WithStack(context.Canceled))
		<-done
	})
	go func() {
		defer close(done)
		for {
			prog, err := pr.Read(context.Background())
			if err != nil {
				return
			}
			for _, log := range prog {
				switch lsys := log.Sys.(type) {
				case client.VertexLog:
					var stream string
					switch lsys.Stream {
					case 1:
						stream = "stdout"
					case 2:
						stream = "stderr"
					default:
						stream = strconv.FormatInt(int64(lsys.Stream), 10)
					}
					t.Logf("(%v) %s", stream, lsys.Data)
				default:
					t.Logf("(%T) %+v", log.Sys, log)
				}
			}
		}
	}()
	return ctx
}
