package core

import (
	"context"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/stretchr/testify/require"
)

func producerGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_ALLOW_PROTOCOL=file:git:http:https")
	data, err := cmd.CombinedOutput()
	require.NoError(t, err, string(data))
	return strings.TrimSpace(string(data))
}
func producerGitSnapshot(t *testing.T, ctx context.Context, store *testutil.Store, build func(string)) bkcache.ImmutableRef {
	t.Helper()
	mutable, err := store.Manager.New(ctx, nil)
	require.NoError(t, err)
	require.NoError(t, MountRef(ctx, mutable, func(root string, _ *mount.Mount) error { build(root); return nil }))
	snapshot, err := mutable.Commit(ctx)
	require.NoError(t, err)
	return snapshot
}
func producerDirectoryResult(t *testing.T, ctx context.Context, cache *dagql.Cache, srv *dagql.Server, field, path string, snapshot bkcache.ImmutableRef) dagql.ObjectResult[*Directory] {
	t.Helper()
	dir := freshProducerDirectory()
	dir.Dir.setValue(path)
	dir.Snapshot.setValue(snapshot)
	return attachTransferObject(t, ctx, cache, srv, "producer-execution", field, dir)
}
func producerLocalRepo(t *testing.T, ctx context.Context, cache *dagql.Cache, srv *dagql.Server, field string, dir dagql.ObjectResult[*Directory]) dagql.ObjectResult[*GitRepository] {
	t.Helper()
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*GitRepository]{}))
	repo := &GitRepository{Backend: &LocalGitRepository{Directory: dir}, Remote: &gitutil.Remote{}}
	return attachTransferObject(t, ctx, cache, srv, "producer-execution", field, repo)
}
func decodeDirectoryProducer(t *testing.T, ctx context.Context, cache *dagql.Cache, srv *dagql.Server, recipe Lazy[*Directory]) Lazy[*Directory] {
	t.Helper()
	kind, raw, err := encodePersistedDirectoryLazy(ctx, dagql.NewPersistEncodeContext(cache, 0, nil), recipe)
	require.NoError(t, err)
	output, err := decodePersistedDirectoryLazy(ctx, dagql.NewPersistDecodeContext(srv, 0, nil), kind, raw)
	require.NoError(t, err)
	return output
}
func readProducerDirectoryFile(t *testing.T, ctx context.Context, dir *Directory, name string) []byte {
	t.Helper()
	path, snapshot, err := producedDirectoryOutput(dir)
	require.NoError(t, err)
	var data []byte
	require.NoError(t, MountRef(ctx, snapshot, func(root string, _ *mount.Mount) error {
		data, err = os.ReadFile(filepath.Join(root, path, name))
		return err
	}))
	return data
}
func TestGitCompletedProducersEvaluate(t *testing.T) {
	ctx, store, cache, srv, _ := executionFixture(t)
	var sha string
	snapshot := producerGitSnapshot(t, ctx, store, func(root string) {
		root = filepath.Join(root, "selected")
		require.NoError(t, os.Mkdir(root, 0755))
		producerGit(t, root, "init", "-b", "main")
		producerGit(t, root, "config", "user.name", "Test")
		producerGit(t, root, "config", "user.email", "test@example.com")
		for name, data := range map[string]string{"tracked": "original", "deleted": "retained", ".gitignore": "ignored\n"} {
			require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(data), 0644))
		}
		producerGit(t, root, "add", ".")
		producerGit(t, root, "commit", "-m", "saved")
		sha = producerGit(t, root, "rev-parse", "HEAD")
		require.NoError(t, os.WriteFile(filepath.Join(root, "tracked"), []byte("staged"), 0644))
		producerGit(t, root, "add", "tracked")
		require.NoError(t, os.WriteFile(filepath.Join(root, "tracked"), []byte("dirty"), 0644))
		require.NoError(t, os.Remove(filepath.Join(root, "deleted")))
		require.NoError(t, os.WriteFile(filepath.Join(root, "untracked"), []byte("untracked"), 0644))
		require.NoError(t, os.WriteFile(filepath.Join(root, "ignored"), []byte("ignored"), 0644))
		require.NoError(t, os.Mkdir(filepath.Join(root, "empty"), 0755))
	})
	dir := producerDirectoryResult(t, ctx, cache, srv, "localSource", "/selected", snapshot)
	repo := producerLocalRepo(t, ctx, cache, srv, "localRepo", dir)
	index := readProducerDirectoryFile(t, ctx, dir.Self(), ".git/index")
	dagql.Fields[*GitRepository]{dagql.NodeFunc("fixtureCleaned", func(ctx context.Context, parent dagql.ObjectResult[*GitRepository], _ struct{}) (dagql.ObjectResult[*Directory], error) {
		return parent.Self().Backend.Cleaned(ctx)
	})}.Install(srv)
	var eager dagql.ObjectResult[*Directory]
	require.NoError(t, srv.Select(ctx, repo, &eager, dagql.Selector{Field: "fixtureCleaned"}))
	lazy := decodeDirectoryProducer(t, ctx, cache, srv, &DirectoryGitCleanedLazy{LazyState: NewLazyState(), Repo: repo})
	output := freshProducerDirectory()
	require.NoError(t, lazy.Evaluate(ctx, output))
	defer output.OnRelease(ctx)
	for _, name := range []string{"tracked", "deleted", "ignored", ".git/index"} {
		require.Equal(t, readProducerDirectoryFile(t, ctx, eager.Self(), name), readProducerDirectoryFile(t, ctx, output, name))
	}
	require.Equal(t, "original", string(readProducerDirectoryFile(t, ctx, output, "tracked")))
	require.Equal(t, index, readProducerDirectoryFile(t, ctx, dir.Self(), ".git/index"))
	require.Equal(t, "dirty", string(readProducerDirectoryFile(t, ctx, dir.Self(), "tracked")))
	path, _ := output.Dir.Peek()
	require.Equal(t, "/selected", path)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*GitRef]{}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*GitCommit]{}))
	for _, repoDiscard := range []bool{false, true} {
		for _, discard := range []bool{false, true} {
			for _, commitTree := range []bool{false, true} {
				name := strings.Join([]string{boolName(repoDiscard), boolName(discard), boolName(commitTree)}, "-")
				t.Run(name, func(t *testing.T) {
					copyRepo := &GitRepository{Backend: repo.Self().Backend, Remote: repo.Self().Remote, DiscardGitDir: repoDiscard}
					savedRepo := attachTransferObject(t, ctx, cache, srv, "producer-execution", "repo"+name, copyRepo)
					refValue := &gitutil.Ref{Name: "refs/heads/main", SHA: sha}
					backend, err := copyRepo.Backend.Get(ctx, refValue)
					require.NoError(t, err)
					var recipe Lazy[*Directory]
					var original *Directory
					if commitTree {
						commit := &GitCommit{Repo: savedRepo, Ref: &gitutil.Ref{SHA: sha}, FetchRef: refValue, Backend: backend}
						parent := attachTransferObject(t, ctx, cache, srv, "producer-execution", "commit"+name, commit)
						recipe = &DirectoryGitCommitTreeLazy{LazyState: NewLazyState(), Commit: parent, DiscardGitDir: discard, Depth: 1, IncludeTags: true}
						original, err = commit.Tree(ctx, srv, discard, 1, true)
					} else {
						ref := &GitRef{Repo: savedRepo, Ref: refValue, Backend: backend}
						parent := attachTransferObject(t, ctx, cache, srv, "producer-execution", "ref"+name, ref)
						recipe = &DirectoryGitTreeLazy{LazyState: NewLazyState(), Ref: parent, DiscardGitDir: discard, Depth: 1, IncludeTags: true}
						original, err = ref.Tree(ctx, srv, discard, 1, true)
					}
					require.NoError(t, err)
					defer original.OnRelease(ctx)
					output := freshProducerDirectory()
					producer := decodeDirectoryProducer(t, ctx, cache, srv, recipe)
					require.NoError(t, producer.Evaluate(ctx, output))
					defer output.OnRelease(ctx)
					require.Equal(t, readProducerDirectoryFile(t, ctx, original, "tracked"), readProducerDirectoryFile(t, ctx, output, "tracked"))
					require.Equal(t, "original", string(readProducerDirectoryFile(t, ctx, output, "tracked")))
					_, snap, err := producedDirectoryOutput(output)
					require.NoError(t, err)
					require.NoError(t, MountRef(ctx, snap, func(root string, _ *mount.Mount) error {
						_, err := os.Stat(filepath.Join(root, ".git"))
						if discard || repoDiscard {
							require.ErrorIs(t, err, os.ErrNotExist)
						} else {
							require.NoError(t, err)
						}
						return nil
					}))
				})
			}
		}
	}
	bare := producerGitSnapshot(t, ctx, store, func(root string) { producerGit(t, root, "init", "--bare") })
	bareDir := producerDirectoryResult(t, ctx, cache, srv, "bareSource", "/", bare)
	bareRepo := producerLocalRepo(t, ctx, cache, srv, "bareRepo", bareDir)
	invalid := decodeDirectoryProducer(t, ctx, cache, srv, &DirectoryGitCleanedLazy{LazyState: NewLazyState(), Repo: bareRepo})
	empty := freshProducerDirectory()
	require.EqualError(t, invalid.Evaluate(ctx, empty), "git cleaned producer: saved input has no worktree")
	_, ready := empty.Snapshot.Peek()
	require.False(t, ready)
}
func boolName(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func TestGitCompletedProducersRemoteEvaluate(t *testing.T) {
	ctx, _, cache, srv, _ := executionFixture(t)
	base := t.TempDir()
	work := filepath.Join(base, "work")
	require.NoError(t, os.Mkdir(work, 0755))
	producerGit(t, work, "init", "-b", "main")
	producerGit(t, work, "config", "user.name", "Test")
	producerGit(t, work, "config", "user.email", "test@example.com")
	require.NoError(t, os.WriteFile(filepath.Join(work, "tracked"), []byte("saved"), 0644))
	producerGit(t, work, "add", ".")
	producerGit(t, work, "commit", "-m", "saved")
	savedSHA := producerGit(t, work, "rev-parse", "HEAD")
	producerGit(t, work, "tag", "v1")
	producerGit(t, base, "clone", "--bare", work, "origin.git")
	gitPath, err := exec.LookPath("git")
	require.NoError(t, err)
	origin := httptest.NewServer(&cgi.Handler{Path: gitPath, Args: []string{"http-backend"}, Env: []string{"GIT_PROJECT_ROOT=" + base, "GIT_HTTP_EXPORT_ALL=1"}})
	defer origin.Close()
	sub := filepath.Join(base, "subwork")
	require.NoError(t, os.Mkdir(sub, 0755))
	producerGit(t, sub, "init", "-b", "main")
	producerGit(t, sub, "config", "user.name", "Test")
	producerGit(t, sub, "config", "user.email", "test@example.com")
	require.NoError(t, os.WriteFile(filepath.Join(sub, "child"), []byte("saved submodule"), 0644))
	producerGit(t, sub, "add", ".")
	producerGit(t, sub, "commit", "-m", "submodule")
	producerGit(t, base, "clone", "--bare", sub, "sub.git")
	producerGit(t, work, "submodule", "add", origin.URL+"/sub.git", "module")
	producerGit(t, work, "commit", "-am", "add submodule")
	savedSHA = producerGit(t, work, "rev-parse", "HEAD")
	producerGit(t, work, "push", filepath.Join(base, "origin.git"), "main")
	url, err := gitutil.ParseURL(origin.URL + "/origin.git")
	require.NoError(t, err)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*GitRepository]{}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*GitRef]{}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*GitCommit]{}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*RemoteGitMirror]{}))
	mirror := attachTransferObject(t, ctx, cache, srv, "producer-execution", "mirror", NewRemoteGitMirror(url.Remote()))
	repo := attachTransferObject(t, ctx, cache, srv, "producer-execution", "remoteRepo", &GitRepository{Backend: &RemoteGitRepository{URL: url, Mirror: mirror}, Remote: &gitutil.Remote{}})
	refValue := &gitutil.Ref{Name: "refs/heads/main", SHA: savedSHA}
	backend, err := repo.Self().Backend.Get(ctx, refValue)
	require.NoError(t, err)
	ref := attachTransferObject(t, ctx, cache, srv, "producer-execution", "remoteRef", &GitRef{Repo: repo, Ref: refValue, Backend: backend})
	commit := attachTransferObject(t, ctx, cache, srv, "producer-execution", "remoteCommit", &GitCommit{Repo: repo, Ref: &gitutil.Ref{SHA: savedSHA}, FetchRef: refValue, Backend: backend})
	require.NoError(t, os.WriteFile(filepath.Join(work, "tracked"), []byte("advanced"), 0644))
	producerGit(t, work, "add", ".")
	producerGit(t, work, "commit", "-m", "advanced")
	producerGit(t, work, "push", filepath.Join(base, "origin.git"), "main")
	for _, recipe := range []Lazy[*Directory]{&DirectoryGitTreeLazy{LazyState: NewLazyState(), Ref: ref, DiscardGitDir: true, Depth: 1, IncludeTags: true}, &DirectoryGitCommitTreeLazy{LazyState: NewLazyState(), Commit: commit, DiscardGitDir: false, Depth: 0}} {
		decoded := decodeDirectoryProducer(t, ctx, cache, srv, recipe)
		output := freshProducerDirectory()
		require.NoError(t, decoded.Evaluate(ctx, output))
		require.Equal(t, "saved", string(readProducerDirectoryFile(t, ctx, output, "tracked")))
		require.Equal(t, "saved submodule", string(readProducerDirectoryFile(t, ctx, output, "module/child")))
		require.NoError(t, output.OnRelease(ctx))
	}
}

func TestGitBundleCompletedProducerEvaluate(t *testing.T) {
	ctx, store, cache, srv, _ := executionFixture(t)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*GitBundle]{}))
	var first, second string
	source := producerGitSnapshot(t, ctx, store, func(root string) {
		producerGit(t, root, "init", "-b", "main")
		producerGit(t, root, "config", "user.name", "Test")
		producerGit(t, root, "config", "user.email", "test@example.com")
		require.NoError(t, os.WriteFile(filepath.Join(root, "tracked"), []byte("first"), 0644))
		producerGit(t, root, "add", ".")
		producerGit(t, root, "commit", "-m", "first")
		first = producerGit(t, root, "rev-parse", "HEAD")
		require.NoError(t, os.WriteFile(filepath.Join(root, "tracked"), []byte("second"), 0644))
		producerGit(t, root, "add", ".")
		producerGit(t, root, "commit", "-m", "second")
		second = producerGit(t, root, "rev-parse", "HEAD")
		producerGit(t, root, "branch", "alternate")
	})
	dir := producerDirectoryResult(t, ctx, cache, srv, "bundleSource", "/", source)
	repo := producerLocalRepo(t, ctx, cache, srv, "bundleRepo", dir)
	for _, incremental := range []bool{false, true} {
		t.Run(boolName(incremental), func(t *testing.T) {
			bundleSnapshot := producerGitSnapshot(t, ctx, store, func(root string) {
				require.NoError(t, MountRef(ctx, source, func(repoRoot string, _ *mount.Mount) error {
					args := []string{"bundle", "create", filepath.Join(root, "repo.bundle"), "main", "alternate"}
					if incremental {
						args = append(args, "^"+first)
					}
					producerGit(t, repoRoot, args...)
					return nil
				}))
			})
			file := freshProducerFile()
			file.File.setValue("/repo.bundle")
			file.Snapshot.setValue(bundleSnapshot)
			fileRes := attachTransferObject(t, ctx, cache, srv, "producer-execution", "bundleFile"+boolName(incremental), file)
			bundle, err := ParseGitBundle(ctx, fileRes)
			require.NoError(t, err)
			require.Len(t, bundle.Refs, 2)
			bundleRes := attachTransferObject(t, ctx, cache, srv, "producer-execution", "bundle"+boolName(incremental), bundle)
			eager, err := ImportGitBundle(ctx, repo.Self(), bundle, "")
			require.NoError(t, err)
			defer eager.OnRelease(ctx)
			producer := decodeDirectoryProducer(t, ctx, cache, srv, &DirectoryGitBundleImportLazy{LazyState: NewLazyState(), Repo: repo, Bundle: bundleRes})
			output := freshProducerDirectory()
			require.NoError(t, producer.Evaluate(ctx, output))
			defer output.OnRelease(ctx)
			for _, dir := range []*Directory{eager, output} {
				_, snapshot, err := producedDirectoryOutput(dir)
				require.NoError(t, err)
				require.NoError(t, MountRef(ctx, snapshot, func(root string, _ *mount.Mount) error {
					require.Equal(t, second, producerGit(t, root, "rev-parse", "refs/heads/main"))
					require.Equal(t, second, producerGit(t, root, "rev-parse", "refs/heads/alternate"))
					require.Equal(t, "second", producerGit(t, root, "show", "refs/heads/main:tracked"))
					return nil
				}))
			}
			if incremental {
				missingSnapshot := producerGitSnapshot(t, ctx, store, func(root string) { producerGit(t, root, "init", "--bare") })
				missingDir := producerDirectoryResult(t, ctx, cache, srv, "missingPrerequisiteDirectory", "/", missingSnapshot)
				missingRepo := producerLocalRepo(t, ctx, cache, srv, "missingPrerequisiteRepo", missingDir)
				missing := freshProducerDirectory()
				err := (&DirectoryGitBundleImportLazy{LazyState: NewLazyState(), Repo: missingRepo, Bundle: bundleRes}).Evaluate(ctx, missing)
				require.ErrorContains(t, err, "is not available from the repository")
				_, ready := missing.Snapshot.Peek()
				require.False(t, ready)
			}
			changed := bundle.Clone()
			changed.ObjectFormat = "sha256"
			changedRes := attachTransferObject(t, ctx, cache, srv, "producer-execution", "changedBundle"+boolName(incremental), changed)
			rejected := freshProducerDirectory()
			err = (&DirectoryGitBundleImportLazy{LazyState: NewLazyState(), Repo: repo, Bundle: changedRes}).Evaluate(ctx, rejected)
			require.ErrorContains(t, err, "header changed")
			_, ready := rejected.Snapshot.Peek()
			require.False(t, ready)
			if !incremental {
				for _, failure := range []string{"ref order", "object format", "malformed", "oversize"} {
					t.Run(failure, func(t *testing.T) {
						inputRepo, inputBundle := repo, bundle.Clone()
						wantErr := "header changed"
						switch failure {
						case "ref order":
							inputBundle.Refs[0], inputBundle.Refs[1] = inputBundle.Refs[1], inputBundle.Refs[0]
						case "object format":
							wrongFormat := producerGitSnapshot(t, ctx, store, func(root string) { producerGit(t, root, "init", "--bare", "--object-format=sha256") })
							inputRepo = producerLocalRepo(t, ctx, cache, srv, "wrongFormatRepo", producerDirectoryResult(t, ctx, cache, srv, "wrongFormatDirectory", "/", wrongFormat))
							wantErr = "object format"
						case "malformed", "oversize":
							badSnapshot := producerGitSnapshot(t, ctx, store, func(root string) {
								name := filepath.Join(root, "bad.bundle")
								require.NoError(t, os.WriteFile(name, []byte("invalid bundle\n"), 0644))
								if failure == "oversize" {
									require.NoError(t, os.Truncate(name, (128<<20)+1))
								}
							})
							badFile := freshProducerFile()
							badFile.File.setValue("/bad.bundle")
							badFile.Snapshot.setValue(badSnapshot)
							inputBundle.File = attachTransferObject(t, ctx, cache, srv, "producer-execution", failure+"File", badFile)
							wantErr = "signature"
							if failure == "oversize" {
								wantErr = "size"
							}
						}
						input := attachTransferObject(t, ctx, cache, srv, "producer-execution", failure+"Bundle", inputBundle)
						decoded := decodeDirectoryProducer(t, ctx, cache, srv, &DirectoryGitBundleImportLazy{LazyState: NewLazyState(), Repo: inputRepo, Bundle: input})
						receiver := freshProducerDirectory()
						privateErr := decoded.Evaluate(ctx, receiver)
						require.ErrorContains(t, privateErr, wantErr)
						_, eagerErr := ImportGitBundle(ctx, inputRepo.Self(), inputBundle, "")
						require.EqualError(t, privateErr, eagerErr.Error())
						_, ready := receiver.Snapshot.Peek()
						require.False(t, ready)
					})
				}
			}
		})
	}
}
