package core

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// These cases need no engine: they exercise the opt-in parsing and the
// dumper's registry with stand-ins for the two calls that reach an engine.

func TestNestedEngineDumpOptIn(t *testing.T) {
	env := func(value string) func(string) string {
		return func(key string) string {
			if key == nestedEngineDumpEnv {
				return value
			}
			return ""
		}
	}
	after, err := nestedEngineDumpAfter(env(""), nil)
	require.NoError(t, err)
	require.Zero(t, after, "unset means off")

	after, err = nestedEngineDumpAfter(env("5m30s"), strings.NewReader(nestedEngineDumpEnv+"=1s\n"))
	require.NoError(t, err)
	require.Equal(t, 5*time.Minute+30*time.Second, after, "the environment wins over the env file")

	after, err = nestedEngineDumpAfter(env(""), strings.NewReader("OTHER=1\n # comment\n"+nestedEngineDumpEnv+" = \"90s\"\n"))
	require.NoError(t, err)
	require.Equal(t, 90*time.Second, after, "engine-dev test --env-file delivers the opt-in as a file")

	for _, bad := range []string{"soon", "0s", "-1m", "5"} {
		_, err = nestedEngineDumpAfter(env(bad), nil)
		require.ErrorContains(t, err, nestedEngineDumpEnv, "a malformed opt-in %q is an error, never a silent off", bad)
	}
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestNestedEngineDumpWritesEveryWatchedEngineOnce(t *testing.T) {
	out := new(lockedBuffer)
	// Already past its deadline, so the dump runs as soon as an engine is
	// watched; the test waits on the dumper's own completion, not on a clock.
	dumper := newNestedEngineDumper(time.Millisecond, time.Now().Add(-time.Second), out)
	ids := map[string]string{"a": "svc-a", "b first life": "svc-b", "b restarted": "svc-b", "stopped": "svc-c", "unreachable": "svc-d"}
	dumper.identify = func(_ context.Context, e nestedEngine) (string, error) { return ids[e.label], nil }
	var fetched sync.Map
	dumper.fetch = func(_ context.Context, e nestedEngine) (string, error) {
		fetched.Store(e.label, true)
		if e.label == "unreachable" {
			return "", errors.New("connection refused")
		}
		return "goroutines of " + ids[e.label], nil
	}

	// Registered before the first watch starts the dumper's goroutine.
	dumper.mu.Lock()
	for i, label := range []string{"a", "b first life", "b restarted", "unreachable"} {
		dumper.engines[100+i] = nestedEngine{label: label}
	}
	dumper.mu.Unlock()
	dumper.watch(nestedEngine{label: "stopped"})()
	select {
	case <-dumper.done:
	case <-time.After(10 * time.Second):
		t.Fatal("the dump never completed")
	}

	got := out.String()
	require.Contains(t, got, "NESTED-ENGINE-DUMP test process still running")
	require.Contains(t, got, "TestNestedEngineDumpWritesEveryWatchedEngineOnce", "the test process's own goroutines are written")
	require.Contains(t, got, "goroutines of svc-a")
	require.Equal(t, 1, strings.Count(got, "goroutines of svc-b"), "a restarted engine is one service and is dumped once")
	require.Contains(t, got, "dump failed: connection refused", "a failed fetch is reported, not dropped")
	_, stopped := fetched.Load("stopped")
	require.False(t, stopped, "an unwatched engine is never bound again")
	require.Contains(t, got, "NESTED-ENGINE-DUMP all dumps written")
}
