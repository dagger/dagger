package dangv2

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	goruntime "runtime"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/introspection"
)

// preparedDangRemountSource is a small module written against the full Dagger
// API, so that its prepared program carries the API's inferred types, as a
// real module's does.
const preparedDangRemountSource = `type Counter {
  pub value: String! {
    "ok"
  }

  pub greeting(name: String!): String! {
    "hello, " + name
  }

  pub base: Container! {
    container.from("alpine")
  }
}
`

// daggerSchemaFixture decodes the Dagger API schema the codegen tests use,
// through the session's schema cache, as loadDangSchema does.
func daggerSchemaFixture(t testing.TB, ctx context.Context) *introspection.Schema {
	t.Helper()
	data, err := os.ReadFile("../../../../cmd/codegen/introspection/testdata/schema.json")
	require.NoError(t, err)
	schema, err := cachedDangSchema(ctx, "dagger-schema-fixture", func(context.Context) (*introspection.Schema, error) {
		var schema introspection.Schema
		return &schema, json.Unmarshal(data, &schema)
	})
	require.NoError(t, err)
	return schema
}

// remountedDangDir writes the module to a fresh directory, as a source that is
// mounted again gets a fresh mount path: an idle shared mount is released
// after DefaultSharedMountLinger, and each local mount is made in a new
// temporary directory.
func remountedDangDir(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.dang"), []byte(preparedDangRemountSource), 0o600))
	return dir
}

// prepareRemounted prepares the same module, by the same source and schema
// identity, at dir, the way runPreparedDangDir does.
func prepareRemounted(ctx context.Context, dir string) (*dang.PreparedDirectory, error) {
	modKey := preparedDangKey("schema-test", "Counter", "Counter")
	key := preparedDangKey("source", "schema", dir)
	return cachedPreparedDangDir(ctx, modKey, key, func(ctx context.Context) (*dang.PreparedDirectory, error) {
		return dang.PrepareDir(ctx, dir)
	})
}

func liveHeap() uint64 {
	goruntime.GC()
	goruntime.GC()
	var stats goruntime.MemStats
	goruntime.ReadMemStats(&stats)
	return stats.HeapAlloc
}

// TestPreparedDangRemountRetention measures what a session retains when one
// unchanged module is called again after its source was remounted.
func TestPreparedDangRemountRetention(t *testing.T) {
	ctx := schemaCacheContext(t)
	schema := daggerSchemaFixture(t, ctx)
	ctx = dang.ContextWithImportConfigs(ctx, dang.ImportConfig{
		Name: "Dagger", Schema: schema, Client: schemaCacheClient("dagger"), AutoImport: true,
	})
	cache, err := dagql.EngineCache(ctx)
	require.NoError(t, err)
	baseEntries := cache.EntryStats().CompletedArbitrary

	const remounts = 16
	var dirs []string
	before := liveHeap()
	for range remounts {
		dir := remountedDangDir(t)
		dirs = append(dirs, dir)
		program, err := prepareRemounted(ctx, dir)
		require.NoError(t, err)
		_, err = program.Evaluate(ctx)
		require.NoError(t, err)
	}
	retained := int64(liveHeap()) - int64(before)
	entries := cache.EntryStats().CompletedArbitrary - baseEntries
	kept := keptPreparedDangPrograms(t, ctx, preparedDangKey("schema-test", "Counter", "Counter"))
	t.Logf("%d remounts of one unchanged module: %d prepared entries holding %d programs, %.2f MiB retained",
		remounts, entries, kept, float64(retained)/(1<<20))
	require.Equal(t, 1, entries, "the session keeps one entry per module")
	require.Equal(t, maxPreparedDangPrograms, kept, "only the most recent mounts' programs are kept")

	// The current mount's program is reused.
	program, err := prepareRemounted(ctx, dirs[remounts-1])
	require.NoError(t, err)
	again, err := prepareRemounted(ctx, dirs[remounts-1])
	require.NoError(t, err)
	require.Same(t, program, again)
}
