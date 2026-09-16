package core

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/stretchr/testify/require"
)

type operationCleanupRef struct {
	*cacheVolumeTestMutableRef
	root                string
	mountErr, commitErr error
	releases, commits   int
}

func (r *operationCleanupRef) Mount(context.Context, bool) (bkcache.MountableRef, error) {
	if r.mountErr != nil {
		return nil, r.mountErr
	}
	return operationTreeMount(r.root), nil
}
func (r *operationCleanupRef) Commit(context.Context) (bkcache.ImmutableRef, error) {
	r.commits++
	if r.commitErr != nil {
		return nil, r.commitErr
	}
	return &operationTreeRef{cacheVolumeTestImmutableRef: &cacheVolumeTestImmutableRef{id: "committed", snapshotID: "committed"}, root: r.root}, nil
}
func (r *operationCleanupRef) Release(ctx context.Context) error {
	if ctx.Err() != nil {
		return errors.New("cleanup context canceled")
	}
	r.releases++
	return nil
}

type operationCleanupManager struct {
	*cacheVolumeTestSnapshotManager
}

func (*operationCleanupManager) Scratch(context.Context) (bkcache.ImmutableRef, error) {
	return &cacheVolumeTestImmutableRef{}, nil
}

func operationCleanupContext(t *testing.T, ref *operationCleanupRef) context.Context {
	t.Helper()
	mgr := &operationCleanupManager{&cacheVolumeTestSnapshotManager{newResult: ref}}
	return ContextWithQuery(t.Context(), &Query{Server: &cacheVolumeTestQueryServer{mockServer: &mockServer{}, cacheManager: mgr}})
}

func TestLazyOperationPathCleanup(t *testing.T) {
	fault := errors.New("injected path failure")
	for _, body := range []string{"blob", "http"} {
		for _, exit := range []string{"mount", "write", "commit", "success"} {
			t.Run(body+"/"+exit, func(t *testing.T) {
				ref := &operationCleanupRef{cacheVolumeTestMutableRef: &cacheVolumeTestMutableRef{}, root: t.TempDir()}
				if exit == "mount" {
					ref.mountErr = fault
				}
				if exit == "commit" {
					ref.commitErr = fault
				}
				ctx := operationCleanupContext(t, ref)
				var err error
				if body == "blob" {
					if exit == "write" {
						require.NoError(t, os.Mkdir(filepath.Join(ref.root, "data"), 0755))
					}
					file := &File{File: new(LazyAccessor[string, *File]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File])}
					lazy := &FileBlobLazy{LazyState: NewLazyState(), Filename: "data", Contents: []byte("body"), Permissions: 0644}
					err = lazy.Evaluate(ctx, file)
					_, ready := file.Snapshot.Peek()
					require.Equal(t, exit == "success", ready)
				} else {
					if exit != "write" {
						require.NoError(t, os.WriteFile(filepath.Join(ref.root, httpStateCanonicalPath), []byte("body"), 0644))
					}
					state := &HTTPState{snapshot: &cacheVolumeTestImmutableRef{}}
					query, e := CurrentQuery(ctx)
					require.NoError(t, e)
					out, e := state.fileResult(ctx, query, "data", 0644)
					err = e
					require.Equal(t, exit == "success", out != nil)
				}
				if exit == "success" {
					require.NoError(t, err)
					require.Zero(t, ref.releases)
				} else {
					require.Error(t, err)
					require.Equal(t, 1, ref.releases)
				}
				if exit == "mount" || exit == "commit" {
					require.ErrorIs(t, err, fault)
				}
			})
		}
	}
	t.Run("cleaned bare alias", func(t *testing.T) {
		ref := &operationCleanupRef{cacheVolumeTestMutableRef: &cacheVolumeTestMutableRef{}, root: t.TempDir()}
		out, err := exec.Command("git", "init", "--bare", ref.root).CombinedOutput()
		require.NoError(t, err, string(out))
		ctx := operationCleanupContext(t, ref)
		cache, err := dagql.NewCache(ctx, "", nil, nil)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, cache.CloseDiscardingPersistence()) })
		ctx = dagql.ContextWithCache(ctx, cache)
		query, err := CurrentQuery(ctx)
		require.NoError(t, err)
		srv := newCoreDagqlServerForTest(t, query)
		srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Directory]{}))
		dir := containerPersistenceTestDirectory("borrowed", "/")
		borrowedReleases := 0
		dir.Snapshot.setValue(&cacheVolumeTestImmutableRef{release: func(context.Context) error { borrowedReleases++; return nil }})
		ancestor := attachTransferObject(t, ctx, cache, srv, "cleanup", "ancestor", containerPersistenceTestDirectory("ancestor", "/"))
		existing := &DirectorySubdirectoryLazy{LazyState: NewLazyState(), Parent: ancestor, Subdir: "."}
		require.NoError(t, evaluatedLazyFixture(dir, existing))
		parent := attachTransferObject(t, ctx, cache, srv, "cleanup", "directory", dir)
		ancestorID := persistedRowID(t, cache, ancestor)
		ownership := func() int64 {
			for _, row := range cache.DebugEGraphSnapshot().Results {
				if row.SharedResultID == ancestorID {
					return row.IncomingOwnershipCount
				}
			}
			t.Fatal("missing ancestor")
			return 0
		}
		before := ownership()
		result, err := (&LocalGitRepository{Directory: parent}).Cleaned(ctx)
		require.NoError(t, err)
		require.Same(t, dir, result.Self())
		require.Equal(t, 1, ref.releases)
		require.Zero(t, ref.commits)
		require.Zero(t, borrowedReleases)
		require.Same(t, existing, dir.Lazy)
		call := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "cleanedAlias", Type: dagql.NewResultCallType(dir.Type())}
		alias, err := cache.GetOrInitCall(ctx, "cleanup", srv, &dagql.CallRequest{ResultCall: call}, func(context.Context) (dagql.AnyResult, error) { return result, nil })
		require.NoError(t, err)
		require.Equal(t, persistedRowID(t, cache, parent), persistedRowID(t, cache, alias))
		require.Equal(t, before, ownership(), "alias attachment added another input owner")
		require.Same(t, existing, dir.Lazy)

	})
}

func TestLazyOperationTemporaryIndexCleanup(t *testing.T) {
	for _, exit := range []string{"copy", "close", "command", "success"} {
		t.Run(exit, func(t *testing.T) {
			tmp, err := os.CreateTemp(t.TempDir(), "dagger-git-index-")
			require.NoError(t, err)
			content := "index"
			if exit == "copy" || exit == "close" {
				require.NoError(t, tmp.Close())
			}
			if exit == "close" {
				content = ""
			}
			ran := false
			fault := errors.New("injected git failure")
			err = withTemporaryGitIndex(strings.NewReader(content), tmp, func(path string) error {
				ran = true
				data, e := os.ReadFile(path)
				require.NoError(t, e)
				require.Equal(t, content, string(data))
				if exit == "command" {
					return fault
				}
				return nil
			})
			if exit == "success" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			if exit == "copy" {
				require.NotContains(t, err.Error(), "\n", "copy keeps its original error message")
			}
			if exit == "command" {
				require.ErrorIs(t, err, fault)
			}
			require.Equal(t, exit == "command" || exit == "success", ran)
			_, err = os.Stat(tmp.Name())
			require.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}
