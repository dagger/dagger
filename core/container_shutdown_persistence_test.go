package core

import (
	"context"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

type shutdownContainerReaderKey struct{}

// Park the read-only mapping inside containerPartComputed, the same method
// CacheDebugValue calls while holding LazyMu. No evaluator or cache operation
// is active during this reader, just as in the opt-in debug snapshot path.
type shutdownContainerReadOp struct {
	ContainerWithLabelLazy
	read func()
}

func (op *shutdownContainerReadOp) ContainerLazyGroups(ctx context.Context, ctr *Container, parts []dagql.PartKey) ([]dagql.LazyGroupKey, error) {
	if ctx.Value(shutdownContainerReaderKey{}) != nil {
		op.read()
	}
	return op.ContainerWithLabelLazy.ContainerLazyGroups(ctx, ctr, parts)
}

// A mutex wait is not a durable synctest wait. Observe the actual blocked
// encoder stack instead of assuming a scheduling delay means it is waiting.
func awaitContainerPersistenceLatch(t *testing.T, done <-chan error) {
	t.Helper()
	var finished bool
	var closeErr error
	var waiting string
	require.Eventually(t, func() bool {
		select {
		case closeErr = <-done:
			finished = true
			return true
		default:
		}
		buf := make([]byte, 256<<10)
		for _, stack := range strings.Split(string(buf[:runtime.Stack(buf, true)]), "\n\n") {
			if strings.Contains(stack, "[sync.Mutex.Lock]") && strings.Contains(stack, "(*Container).lockForPersistence(") {
				waiting = stack
				return true
			}
		}
		return false
	}, 5*time.Second, time.Millisecond, "shutdown encoder did not reach the contended latch")
	require.False(t, finished, "Close returned before the read-only holder released LazyMu: %v", closeErr)
	t.Logf("observed shutdown waiting for the read-only latch:\n%s", waiting)
}

func TestContainerShutdownPersistenceWaitsForReader(t *testing.T) {
	for _, completed := range []bool{false, true} {
		name := "pending"
		if completed {
			name = "completed"
		}
		t.Run(name, func(t *testing.T) {
			env := newPersistedFamiliesTestEnv(t, "shutdown-reader")
			ctx, cache, srv := env.open(t)
			platform := Platform{OS: "linux", Architecture: "amd64"}
			parent := NewContainer(platform)
			parent.Config.WorkingDir = "/persisted"
			parentRes := env.attach(t, ctx, cache, srv, "reader-parent", parent).(dagql.ObjectResult[*Container])
			parentID := persistedRowID(t, cache, parentRes)
			ctr := NewContainer(platform)
			op := &shutdownContainerReadOp{ContainerWithLabelLazy: ContainerWithLabelLazy{
				LazyState: NewLazyState(), Parent: parentRes, Name: "retained", Value: "reader",
			}}
			ctr.Lazy = op
			// Run the real metadata body without the routing layer's final
			// delegation sweep, leaving snapshot groups pending in that case.
			require.NoError(t, op.EvaluateContainerGroup(ctx, ctr, ContainerLazyGroupMetadata))
			if completed {
				require.NoError(t, ctr.Evaluate(ctx))
				require.NotNil(t, ctr.lazyOpForRouting())
				require.Nil(t, ctr.LazyEvalFunc())
				require.Same(t, op, ctr.Lazy)
			}
			res := env.attach(t, ctx, cache, srv, "withLabel", ctr)
			before, err := cache.CapturePersistedRecord(ctx, res)
			require.NoError(t, err)
			require.NoError(t, cache.ReleaseSession(ctx, env.session))

			entered, allow := make(chan struct{}), make(chan struct{})
			finish := sync.OnceFunc(func() { close(allow) })
			defer finish()
			op.read = func() { close(entered); <-allow }
			readDone := make(chan bool, 1)
			go func() {
				readCtx := context.WithValue(ctx, shutdownContainerReaderKey{}, true)
				readDone <- ctr.containerPartComputed(readCtx, op, ContainerPartFS)
			}()
			<-entered
			_, err = cache.CapturePersistedRecord(ctx, res)
			require.ErrorIs(t, err, dagql.ErrPersistStateNotReady, "live capture still refuses a held latch")
			done := make(chan error, 1)
			go func() { done <- cache.Close(ctx) }()
			awaitContainerPersistenceLatch(t, done)
			locked := ctr.lazyOpMu.TryLock()
			if locked {
				ctr.lazyOpMu.Unlock()
			}
			require.True(t, locked, "shutdown must drop lazyOpMu before waiting for LazyMu")
			finish()
			require.Equal(t, completed, <-readDone)
			require.NoError(t, <-done, "a refused capture must not leak a cache operation")
			locked = op.LazyMu.TryLock()
			if locked {
				op.LazyMu.Unlock()
			}
			require.True(t, locked, "shutdown released the state latch")
			for key, group := range op.groups {
				locked = group.mu.TryLock()
				if locked {
					group.mu.Unlock()
				}
				require.True(t, locked, "shutdown released group %s", key)
			}

			ctx, cache, srv = env.open(t) // asserts no unclean-shutdown reset
			loaded, err := cache.LoadResultByResultID(ctx, env.session, srv, before.ResultID)
			require.NoError(t, err)
			after, err := cache.CapturePersistedRecord(ctx, loaded)
			require.NoError(t, err)
			require.Equal(t, before.Envelope, after.Envelope, "operation inputs and pending/completed payloads survive")
			require.Equal(t, before.SnapshotLinks, after.SnapshotLinks)
			if completed {
				found := false
				for _, row := range cache.DebugEGraphSnapshot().Results {
					if row.SharedResultID == parentID {
						found = true
						require.False(t, row.HasValue, "loading a completed operation must not decode its parent")
					}
				}
				require.True(t, found, "the original operation dependency remains retained")
			}
		})
	}
}
