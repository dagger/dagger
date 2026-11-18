package dangv2

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/dang"
	"golang.org/x/sync/errgroup"
)

func TestPreparedDangSingleflightFreshClientsAndRelease(t *testing.T) {
	ctx := schemaCacheContext(t)
	schema, err := dang.SchemaFromSDL("type Query { value: String! }", "schema.graphqls")
	require.NoError(t, err)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.dang"), []byte("import Session\nresult: String! = Session.value\n"), 0o600))
	var builds atomic.Int32
	build := func(ctx context.Context) (*dang.PreparedDirectory, error) {
		builds.Add(1)
		return dang.PrepareDir(ctx, dir)
	}
	modKey := preparedDangKey("schema-test", "module", "original")
	key := preparedDangKey("source-a", "schema-a", dir)
	var group errgroup.Group
	for index := range 12 {
		group.Go(func() error {
			name := fmt.Sprintf("client-%d", index)
			callCtx := dang.ContextWithImportConfigs(ctx, dang.ImportConfig{Name: "Session", Schema: schema, Client: schemaCacheClient(name)})
			program, err := cachedPreparedDangDir(callCtx, modKey, key, build)
			if err != nil {
				return err
			}
			values, err := program.Evaluate(callCtx)
			if err != nil {
				return err
			}
			result, found, err := values.Lookup(callCtx, "result")
			if err != nil || !found || result.(dang.StringValue).Val != name {
				return fmt.Errorf("wrong invocation value %v, found %t: %w", result, found, err)
			}
			return nil
		})
	}
	require.NoError(t, group.Wait())
	require.EqualValues(t, 1, builds.Load())
	for index := range 3 {
		parts := []string{"schema-test", "module", "original"}
		parts[index] += "-different"
		require.NotEqual(t, modKey, preparedDangKey(parts...), "module key input %d was ignored", index)
		parts = []string{"source-a", "schema-a", dir}
		parts[index] += "-different"
		require.NotEqual(t, key, preparedDangKey(parts...), "compilation key input %d was ignored", index)
	}
	failure := errors.New("compilation failed")
	_, err = cachedPreparedDangDir(ctx, modKey, "prepared-retry", func(context.Context) (*dang.PreparedDirectory, error) { return nil, failure })
	require.ErrorIs(t, err, failure)
	callCtx := dang.ContextWithImportConfigs(ctx, dang.ImportConfig{Name: "Session", Schema: schema, Client: schemaCacheClient("retry")})
	_, err = cachedPreparedDangDir(callCtx, modKey, "prepared-retry", build)
	require.NoError(t, err)
	cache, err := dagql.EngineCache(ctx)
	require.NoError(t, err)
	require.NoError(t, cache.ReleaseSession(ctx, "schema-test"))
	require.NoError(t, cache.WaitSessionRelease(ctx, "schema-test"))
	require.Zero(t, cache.EntryStats().CompletedArbitrary)
	require.Zero(t, cache.EntryStats().OngoingArbitrary)
}

// preparedDangProgramsOf returns the compilations the session keeps for the
// module modKey.
func preparedDangProgramsOf(t *testing.T, ctx context.Context, modKey string) *preparedDangPrograms {
	t.Helper()
	cache, err := dagql.EngineCache(ctx)
	require.NoError(t, err)
	result, err := cache.GetOrInitArbitrary(ctx, "schema-test", "native-dang-prepared-module:"+modKey, func(context.Context) (any, error) {
		return nil, errors.New("module has no compilations")
	})
	require.NoError(t, err)
	return result.Value().(*preparedDangPrograms)
}

// keptPreparedDangPrograms reports how many compilations the session keeps
// for the module modKey.
func keptPreparedDangPrograms(t *testing.T, ctx context.Context, modKey string) int {
	t.Helper()
	programs := preparedDangProgramsOf(t, ctx, modKey)
	programs.mu.Lock()
	defer programs.mu.Unlock()
	return len(programs.builds)
}

func TestPreparedDangKeepsRecentCompilations(t *testing.T) {
	ctx := schemaCacheContext(t)
	var builds atomic.Int32
	prepare := func(context.Context) (*dang.PreparedDirectory, error) {
		builds.Add(1)
		return &dang.PreparedDirectory{}, nil
	}
	get := func(modKey, key string) *dang.PreparedDirectory {
		program, err := cachedPreparedDangDir(ctx, modKey, key, prepare)
		require.NoError(t, err)
		return program
	}
	first := get("module", "key-0")
	require.Same(t, first, get("module", "key-0"))
	require.EqualValues(t, 1, builds.Load())
	for index := 1; index < maxPreparedDangPrograms; index++ {
		get("module", fmt.Sprintf("key-%d", index))
	}
	require.EqualValues(t, maxPreparedDangPrograms, builds.Load())
	// Using key-0 again makes key-1 the least recently used.
	require.Same(t, first, get("module", "key-0"))
	get("module", "key-new")
	require.EqualValues(t, maxPreparedDangPrograms+1, builds.Load())
	require.Equal(t, maxPreparedDangPrograms, keptPreparedDangPrograms(t, ctx, "module"))
	require.Same(t, first, get("module", "key-0"), "a recently used compilation is kept")
	require.EqualValues(t, maxPreparedDangPrograms+1, builds.Load())
	get("module", "key-1")
	require.EqualValues(t, maxPreparedDangPrograms+2, builds.Load(), "the least recently used compilation was dropped")

	// Another module has a bound of its own.
	get("other-module", "key-0")
	require.EqualValues(t, maxPreparedDangPrograms+3, builds.Load())
	require.Equal(t, maxPreparedDangPrograms, keptPreparedDangPrograms(t, ctx, "module"))
	require.Equal(t, 1, keptPreparedDangPrograms(t, ctx, "other-module"))

	cache, err := dagql.EngineCache(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, cache.EntryStats().CompletedArbitrary, "a session keeps one entry per module")
	require.NoError(t, cache.ReleaseSession(ctx, "schema-test"))
	require.NoError(t, cache.WaitSessionRelease(ctx, "schema-test"))
	require.Zero(t, cache.EntryStats().CompletedArbitrary)
}

// A call waiting on a preparation whose own caller gave up prepares again
// rather than failing with that caller's cancellation.
func TestPreparedDangWaiterOutlivesCanceledPreparation(t *testing.T) {
	ctx := schemaCacheContext(t)
	ownerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	started := make(chan struct{})
	ownerErr := make(chan error, 1)
	go func() {
		_, err := cachedPreparedDangDir(ownerCtx, "module", "key", func(ctx context.Context) (*dang.PreparedDirectory, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		})
		ownerErr <- err
	}()
	<-started
	want := &dang.PreparedDirectory{}
	waiter := make(chan error, 1)
	var got *dang.PreparedDirectory
	go func() {
		var err error
		got, err = cachedPreparedDangDir(ctx, "module", "key", func(context.Context) (*dang.PreparedDirectory, error) {
			return want, nil
		})
		waiter <- err
	}()
	// Let the waiter join the preparation in flight before it fails.
	programs := preparedDangProgramsOf(t, ctx, "module")
	require.Eventually(t, func() bool {
		programs.mu.Lock()
		defer programs.mu.Unlock()
		return len(programs.builds) == 1 && programs.builds[0].joined == 1
	}, 10*time.Second, time.Millisecond)
	cancel()
	require.ErrorIs(t, <-ownerErr, context.Canceled)
	require.NoError(t, <-waiter)
	require.Same(t, want, got)
	require.Equal(t, 1, keptPreparedDangPrograms(t, ctx, "module"))
}
