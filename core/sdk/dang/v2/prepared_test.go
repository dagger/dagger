package dangv2

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

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
	key := preparedDangKey("schema-test", "source-a", "schema-a", "module", "original", dir)
	var group errgroup.Group
	for index := range 12 {
		group.Go(func() error {
			name := fmt.Sprintf("client-%d", index)
			callCtx := dang.ContextWithImportConfigs(ctx, dang.ImportConfig{Name: "Session", Schema: schema, Client: schemaCacheClient(name)})
			program, err := cachedPreparedDangDir(callCtx, key, build)
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
	for index := range 6 {
		parts := []string{"schema-test", "source-a", "schema-a", "module", "original", dir}
		parts[index] += "-different"
		require.NotEqual(t, key, preparedDangKey(parts...), "key input %d was ignored", index)
	}
	failure := errors.New("compilation failed")
	_, err = cachedPreparedDangDir(ctx, "prepared-retry", func(context.Context) (*dang.PreparedDirectory, error) { return nil, failure })
	require.ErrorIs(t, err, failure)
	callCtx := dang.ContextWithImportConfigs(ctx, dang.ImportConfig{Name: "Session", Schema: schema, Client: schemaCacheClient("retry")})
	_, err = cachedPreparedDangDir(callCtx, "prepared-retry", build)
	require.NoError(t, err)
	cache, err := dagql.EngineCache(ctx)
	require.NoError(t, err)
	require.NoError(t, cache.ReleaseSession(ctx, "schema-test"))
	require.NoError(t, cache.WaitSessionRelease(ctx, "schema-test"))
	require.Zero(t, cache.EntryStats().CompletedArbitrary)
	require.Zero(t, cache.EntryStats().OngoingArbitrary)
}
