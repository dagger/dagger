package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func storedSnapshotTestValue(kind, id, path string, restored bool) dagql.Typed {
	platform := Platform{OS: "linux", Architecture: "arm64"}
	if kind == "Directory" {
		dir := &Directory{Platform: platform, Dir: new(LazyAccessor[string, *Directory]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory])}
		dir.Dir.setValue(path)
		if restored {
			dir.stored = &storedSnapshot{SnapshotID: id}
			dir.Lazy = &DirectoryRestoreLazy{LazyState: NewLazyState()}
		} else {
			dir.Snapshot.setValue(&cacheVolumeTestImmutableRef{id: id, snapshotID: id})
		}
		return dir
	}
	file := &File{Platform: platform, File: new(LazyAccessor[string, *File]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File])}
	file.File.setValue(path)
	if restored {
		file.stored = &storedSnapshot{SnapshotID: id}
		file.Lazy = &FileRestoreLazy{LazyState: NewLazyState()}
	} else {
		file.Snapshot.setValue(&cacheVolumeTestImmutableRef{id: id, snapshotID: id})
	}
	return file
}

func attachStoredSnapshotTestValue(t *testing.T, ctx context.Context, cache *dagql.Cache, srv *dagql.Server, session, field string, value dagql.Typed, persist bool) dagql.AnyResult {
	t.Helper()
	frame := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: field, Type: dagql.NewResultCallType(value.Type())}
	res, err := cache.GetOrInitCall(ctx, session, srv, &dagql.CallRequest{ResultCall: frame, IsPersistable: persist}, func(context.Context) (dagql.AnyResult, error) {
		switch value := value.(type) {
		case *Directory:
			return dagql.NewObjectResultForCall(value, srv, frame)
		case *File:
			return dagql.NewObjectResultForCall(value, srv, frame)
		default:
			return nil, fmt.Errorf("unexpected test value %T", value)
		}
	})
	require.NoError(t, err)
	return res
}

func storedSnapshotTestPath(ctx context.Context, res dagql.AnyResult) (string, error) {
	switch res := res.(type) {
	case dagql.ObjectResult[*Directory]:
		return res.Self().PathOrEval(ctx, res)
	case dagql.ObjectResult[*File]:
		return res.Self().PathOrEval(ctx, res)
	default:
		return "", fmt.Errorf("unexpected test result %T", res)
	}
}

func storedSnapshotTestOpen(res dagql.AnyResult) (bkcache.ImmutableRef, bool) {
	switch value := res.Unwrap().(type) {
	case *Directory:
		return value.Snapshot.Peek()
	case *File:
		return value.Snapshot.Peek()
	default:
		panic("unexpected test result")
	}
}

func TestRestoredSnapshotRoundTrip(t *testing.T) {
	for _, kind := range []string{"Directory", "File"} {
		for _, path := range []string{"", "/", "/nested/name"} {
			t.Run(kind+path, func(t *testing.T) {
				db := filepath.Join(t.TempDir(), "cache.db")
				ctx, cache, srv := containerPersistenceTestCache(t, db, newContainerPersistenceTestSnapshots(), "a")
				value := storedSnapshotTestValue(kind, "saved", path, false)
				res := attachStoredSnapshotTestValue(t, ctx, cache, srv, "a", "saved", value, true)
				id, err := cache.PersistedResultID(res)
				require.NoError(t, err)
				original, err := value.(dagql.PersistedObject).EncodePersistedObject(ctx, cache)
				require.NoError(t, err)
				require.NoError(t, cache.ReleaseSession(ctx, "a"))
				require.NoError(t, cache.Close(ctx))
				for _, session := range []string{"b", "c"} {
					manager := newContainerPersistenceTestSnapshots()
					ctx, cache, srv = containerPersistenceTestCache(t, db, manager, session)
					res, err = cache.LoadResultByResultID(ctx, session, srv, id)
					require.NoError(t, err)
					_, open := storedSnapshotTestOpen(res)
					require.False(t, open)
					got, err := storedSnapshotTestPath(ctx, res)
					require.NoError(t, err)
					require.Equal(t, path, got)
					require.True(t, dagql.HasPendingLazyEvaluation(res))
					require.False(t, dagql.HasPendingLazyComputation(res))
					links := res.Unwrap().(dagql.PersistedSnapshotRefLinkProvider).PersistedSnapshotRefLinks()
					require.Equal(t, []dagql.PersistedSnapshotRefLink{{Role: "snapshot", RefKey: "saved"}}, links)
					require.Contains(t, manager.owners, "dagql/result/"+fmt.Sprint(id)+"/snapshot")
					encoded, err := res.Unwrap().(dagql.PersistedObject).EncodePersistedObject(ctx, cache)
					require.NoError(t, err)
					require.Equal(t, original, encoded)
					require.Zero(t, manager.openCount("saved"))
					if session == "c" {
						require.NoError(t, cache.Evaluate(ctx, res))
						require.NoError(t, cache.Evaluate(ctx, res))
						snapshot, open := storedSnapshotTestOpen(res)
						require.True(t, open)
						require.Equal(t, "saved", snapshot.SnapshotID())
						require.Equal(t, 1, manager.openCount("saved"))
						encoded, err = res.Unwrap().(dagql.PersistedObject).EncodePersistedObject(ctx, cache)
						require.NoError(t, err)
						require.Equal(t, original, encoded)
					}
					require.NoError(t, cache.ReleaseSession(ctx, session))
					require.NoError(t, cache.Close(ctx))
				}
			})
		}
	}
}

func TestRestoredSnapshotFailureAndConcurrentDemand(t *testing.T) {
	for _, kind := range []string{"Directory", "File"} {
		t.Run(kind, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				manager := newContainerPersistenceTestSnapshots()
				ctx, cache, srv := containerPersistenceTestCache(t, "", manager, "retry")
				res := attachStoredSnapshotTestValue(t, ctx, cache, srv, "retry", "saved", storedSnapshotTestValue(kind, "saved", "/", true), false)
				failure := errors.New("snapshot unavailable")
				started, allow := make(chan struct{}), make(chan struct{})
				var attempts atomic.Int32
				manager.beforeOpen = func(context.Context, string) error {
					if attempts.Add(1) <= 2 {
						return failure
					}
					close(started)
					<-allow
					return nil
				}
				for want := 1; want <= 2; want++ {
					require.ErrorIs(t, cache.Evaluate(ctx, res), failure)
					require.Equal(t, want, manager.openCount("saved"))
					_, open := storedSnapshotTestOpen(res)
					require.False(t, open)
				}
				done := make(chan error, 8)
				go func() { done <- cache.Evaluate(ctx, res) }()
				<-started
				for range 7 {
					go func() { done <- cache.Evaluate(ctx, res) }()
				}
				synctest.Wait()
				require.Equal(t, 3, manager.openCount("saved"))
				require.False(t, dagql.HasPendingLazyComputation(res))
				close(allow)
				for range 8 {
					require.NoError(t, <-done)
				}
				require.NoError(t, cache.Evaluate(ctx, res))
				require.Equal(t, 3, manager.openCount("saved"))
			})
		})
	}
}

func TestRestoredSnapshotUsageAndRelease(t *testing.T) {
	for _, kind := range []string{"Directory", "File"} {
		for _, open := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/open=%v", kind, open), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					manager := newContainerPersistenceTestSnapshots()
					manager.snapshotSizes = map[string]int64{"saved": 42}
					ctx, cache, srv := containerPersistenceTestCache(t, "", manager, "owner")
					value := storedSnapshotTestValue(kind, "saved", "/", true)
					res := attachStoredSnapshotTestValue(t, ctx, cache, srv, "owner", "saved", value, false)
					usage := value.(interface {
						CacheUsageIdentities() []string
						CacheUsageSize(context.Context, dagql.CacheUsageSizeProvider, string) (int64, bool, error)
					})
					require.Equal(t, []string{"saved"}, usage.CacheUsageIdentities())
					_, known, err := usage.CacheUsageSize(ctx, nil, "saved")
					require.NoError(t, err)
					require.False(t, known)
					_, known, err = usage.CacheUsageSize(ctx, manager, "other")
					require.NoError(t, err)
					require.False(t, known)
					size, known, err := usage.CacheUsageSize(ctx, manager, "saved")
					require.NoError(t, err)
					require.True(t, known)
					require.EqualValues(t, 42, size)
					require.Zero(t, manager.openCount("saved"))
					if open {
						require.NoError(t, cache.Evaluate(ctx, res))
						size, known, err = usage.CacheUsageSize(ctx, nil, "saved")
						require.NoError(t, err)
						require.True(t, known)
						require.Zero(t, size, "open handle supplies its own size")
					}
					require.NoError(t, cache.ReleaseSession(ctx, "owner"))
					synctest.Wait()
					require.Empty(t, manager.owners)
					if open {
						require.Equal(t, 1, manager.releases["saved"])
					} else {
						require.Zero(t, manager.releases["saved"])
					}
				})
			})
		}
	}
}

func TestRestoredSnapshotReportingAndBookkeepingRetry(t *testing.T) {
	for _, kind := range []string{"Directory", "File"} {
		t.Run(kind, func(t *testing.T) {
			manager := newContainerPersistenceTestSnapshots()
			ctx, cache, srv := containerPersistenceTestCache(t, "", manager, "report")
			recorder := tracetest.NewSpanRecorder()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
			t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })
			ctx, producer := provider.Tracer("restore").Start(ctx, "produce")
			res := attachStoredSnapshotTestValue(t, ctx, cache, srv, "report", "saved", storedSnapshotTestValue(kind, "saved", "/", true), false)
			producer.End()
			status := &telemetryTestSpan{}
			recordStatus(ctx, res, status, true, nil)
			require.True(t, containerPersistenceSpanAttrs(status.attrs)[telemetry.CachedAttr].AsBool())
			failure := errors.New("operation cleanup failed")
			var releases atomic.Int32
			ctx = dagql.ContextWithOperationLeaseProvider(ctx, dagql.OperationLeaseProviderFunc(func(ctx context.Context) (context.Context, func(context.Context) error, error) {
				return ctx, func(context.Context) error {
					if releases.Add(1) == 1 {
						return failure
					}
					return nil
				}, nil
			}))
			ctx, consumer := provider.Tracer("restore").Start(ctx, "consume")
			require.ErrorIs(t, cache.Evaluate(ctx, res), failure)
			require.True(t, dagql.HasPendingLazyEvaluation(res))
			require.False(t, dagql.HasPendingLazyComputation(res))
			require.NoError(t, cache.Evaluate(ctx, res))
			require.Equal(t, 1, manager.openCount("saved"))
			consumer.End()
			var opens []sdktrace.ReadOnlySpan
			for _, span := range recorder.Ended() {
				if span.Name() == "open stored part (snapshot)" {
					opens = append(opens, span)
				}
			}
			require.Len(t, opens, 2)
			require.True(t, containerPersistenceSpanAttrs(opens[1].Attributes())[telemetry.CachedAttr].AsBool())
		})
	}
}

type storedSnapshotTestLazy[T dagql.Typed] struct {
	LazyState
	run func(T) error
}

func (lazy *storedSnapshotTestLazy[T]) Evaluate(ctx context.Context, value T) error {
	return lazy.LazyState.Evaluate(ctx, "test", func(context.Context) error { return lazy.run(value) })
}
func (*storedSnapshotTestLazy[T]) AttachDependencies(context.Context, func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	return nil, nil
}
func (*storedSnapshotTestLazy[T]) EncodePersisted(context.Context, dagql.PersistedObjectCache) (json.RawMessage, error) {
	return nil, errors.New("test lazy has no encoding")
}

func TestRestoredSnapshotPathOrEvalFreshPaths(t *testing.T) {
	ctx, cache, srv := containerPersistenceTestCache(t, "", newContainerPersistenceTestSnapshots(), "paths")
	for _, kind := range []string{"Directory", "File"} {
		value := storedSnapshotTestValue(kind, "unused", "seed", false)
		switch value := value.(type) {
		case *Directory:
			value.Lazy = &storedSnapshotTestLazy[*Directory]{LazyState: NewLazyState(), run: func(dir *Directory) error { dir.Dir.setValue("final"); return nil }}
		case *File:
			value.Lazy = &storedSnapshotTestLazy[*File]{LazyState: NewLazyState(), run: func(file *File) error { file.File.setValue("final"); return nil }}
		}
		res := attachStoredSnapshotTestValue(t, ctx, cache, srv, "paths", kind, value, false)
		require.True(t, dagql.HasPendingLazyComputation(res))
		got, err := storedSnapshotTestPath(ctx, res)
		require.NoError(t, err)
		require.Equal(t, "final", got)
	}
}

func TestSourceFilePathsMixedStoredAndFresh(t *testing.T) {
	manager := newContainerPersistenceTestSnapshots()
	ctx, cache, srv := containerPersistenceTestCache(t, "", manager, "sources")
	stored := attachStoredSnapshotTestValue(t, ctx, cache, srv, "sources", "stored", storedSnapshotTestValue("File", "saved", "/saved.txt", true), false).(dagql.ObjectResult[*File])
	fresh := storedSnapshotTestValue("File", "unused", "seed", false).(*File)
	var runs int
	fresh.Lazy = &storedSnapshotTestLazy[*File]{LazyState: NewLazyState(), run: func(file *File) error { runs++; file.File.setValue("fresh.txt"); return nil }}
	freshRes := attachStoredSnapshotTestValue(t, ctx, cache, srv, "sources", "fresh", fresh, false).(dagql.ObjectResult[*File])
	paths, err := SourceFilePaths(ctx, []dagql.ObjectResult[*File]{stored, freshRes})
	require.NoError(t, err)
	require.Equal(t, []string{"/saved.txt", "fresh.txt"}, paths)
	require.Equal(t, 1, runs)
	require.Zero(t, manager.openCount("saved"))
}

func TestRestoredSnapshotAttemptLifetime(t *testing.T) {
	for _, kind := range []string{"Directory", "File"} {
		for _, release := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/release=%v", kind, release), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					manager := newContainerPersistenceTestSnapshots()
					started, allow := make(chan struct{}), make(chan struct{})
					manager.beforeOpen = func(ctx context.Context, _ string) error {
						close(started)
						select {
						case <-allow:
							return nil
						case <-ctx.Done():
							return context.Cause(ctx)
						}
					}
					ctx, cache, srv := containerPersistenceTestCache(t, "", manager, "lifetime")
					res := attachStoredSnapshotTestValue(t, ctx, cache, srv, "lifetime", "saved", storedSnapshotTestValue(kind, "saved", "/", true), false)
					leaderCtx, cancel := context.WithCancelCause(ctx)
					defer cancel(nil)
					leaderDone := make(chan error, 1)
					go func() { leaderDone <- cache.Evaluate(leaderCtx, res) }()
					<-started
					waiterDone := make(chan error, 1)
					if release {
						require.NoError(t, cache.ReleaseSession(ctx, "lifetime"))
						synctest.Wait()
						require.Positive(t, cache.Size(), "attempt retains the owner while opening")
					} else {
						go func() { waiterDone <- cache.Evaluate(ctx, res) }()
						synctest.Wait()
						cause := errors.New("leader stopped waiting")
						cancel(cause)
						require.ErrorIs(t, <-leaderDone, cause)
					}
					close(allow)
					if release {
						require.ErrorIs(t, <-leaderDone, dagql.ErrCacheSessionReleased)
					} else {
						require.NoError(t, <-waiterDone)
					}
					synctest.Wait()
					require.Equal(t, 1, manager.openCount("saved"))
					if release {
						require.Zero(t, cache.Size())
						require.Equal(t, 1, manager.releases["saved"])
						require.Empty(t, manager.owners)
					}
				})
			})
		}
	}
}
