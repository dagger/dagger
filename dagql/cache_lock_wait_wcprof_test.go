package dagql

import (
	"bytes"
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine/wcprof"
)

// A profiled call that waits for the cache's e-graph lock shows the wait in
// the profile, as a lock wait of the call's op on dagql.egraph.
func TestCallWaitsForEgraphLockWcprof(t *testing.T) {
	ctx := cacheTestContext(context.Background())
	c, err := NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	ctx = ContextWithCache(ctx, c)
	wcprof.EnsureRecorder()
	ctx = wcprof.ContextWithProfiling(ctx)

	call := cacheTestIntCall("lockWaitCall")
	c.egraphMu.Lock()
	done := make(chan error)
	go func() {
		_, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{},
			&CallRequest{ResultCall: call, ConcurrencyKey: "test-session"},
			func(context.Context) (AnyResult, error) {
				return cacheTestDetachedResult(call, NewInt(1)), nil
			})
		done <- err
	}()
	deadline := time.Now().Add(10 * time.Second)
	for !blockedIn("wcprof.ReadLock(") {
		require.True(t, time.Now().Before(deadline), "the call never waited in wcprof.ReadLock")
		runtime.Gosched()
	}
	c.egraphMu.Unlock()
	require.NoError(t, <-done)

	var buf bytes.Buffer
	require.NoError(t, wcprof.Active().WriteDump(&buf, false))
	header, events, err := wcprof.ReadDump(&buf)
	require.NoError(t, err)
	calls := map[uint64]bool{}
	for _, ev := range events {
		if ev.Type == "op" && ev.OpKind == wcprof.OpKindCall.String() && header.Strings[ev.ClassID] == profCallClass(call) {
			calls[ev.OpID] = true
		}
	}
	require.NotEmpty(t, calls, "no op for the call")
	var waited bool
	for _, ev := range events {
		if ev.Type == "wait" && calls[ev.ParentID] && header.Strings[ev.IdentID] == egraphLockIdent {
			require.Equal(t, wcprof.WaitReasonLock.String(), ev.Reason)
			require.LessOrEqual(t, ev.StartNS, ev.EndNS)
			waited = true
		}
	}
	require.True(t, waited, "no lock wait on %s under the call's op", egraphLockIdent)
}

// blockedIn reports whether some goroutine is blocked on a lock inside fn.
func blockedIn(fn string) bool {
	buf := make([]byte, 1<<20)
	for _, stack := range strings.Split(string(buf[:runtime.Stack(buf, true)]), "\n\n") {
		if strings.Contains(stack, fn) && strings.Contains(stack, "sync.RWMutex") {
			return true
		}
	}
	return false
}
