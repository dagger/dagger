package dangv2

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
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

// useStagedDangDirs stages sources under a directory of the test's, keeping
// at most limit unused ones, until the test ends.
func useStagedDangDirs(t testing.TB, limit int) *stagedDangDirs {
	t.Helper()
	saved := stagedDangSources
	stagedDangSources = newStagedDangDirs(t.TempDir(), limit)
	t.Cleanup(func() { stagedDangSources = saved })
	return stagedDangSources
}

// remountedDangDir writes the module to a fresh directory, at its source
// subpath, as a source that is mounted again gets a fresh mount path: an idle
// shared mount is released after DefaultSharedMountLinger, and each local
// mount is made in a new temporary directory. It returns the source
// directory.
func remountedDangDir(t testing.TB, source string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), ".dagger", "modules", "counter")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.dang"), []byte(source), 0o600))
	return dir
}

// prepareRemounted prepares the module whose source is mounted at srcDir the
// way the runtime does (runStagedDangSource, runPreparedDangDir): from where
// it is staged, by the staged source's identity. It returns the program and
// the directory it was compiled from.
func prepareRemounted(ctx context.Context, srcDir string) (*dang.PreparedDirectory, string, error) {
	source, ok := stageDangSource(ctx, srcDir, "counter", ".dagger/modules/counter")
	if !ok {
		return nil, "", os.ErrNotExist
	}
	defer source.release()
	ctx = dang.ContextWithVersion(ctx, source.version)
	modKey := preparedDangKey("schema-test", "Counter", "Counter")
	key := preparedDangCompilationKey(source, "v1.0.0", "schema")
	program, err := cachedPreparedDangDir(ctx, modKey, key, func(ctx context.Context) (*dang.PreparedDirectory, error) {
		return dang.PrepareDir(ctx, source.dir)
	})
	return program, source.dir, err
}

func liveHeap() uint64 {
	goruntime.GC()
	goruntime.GC()
	var stats goruntime.MemStats
	goruntime.ReadMemStats(&stats)
	return stats.HeapAlloc
}

// An unchanged module called again after its source was remounted reuses its
// compilation, which names the same files however often the source is
// mounted; an edit compiles again.
func TestPreparedDangRemountReusesCompilation(t *testing.T) {
	staged := useStagedDangDirs(t, maxStagedDangSources)
	ctx := schemaCacheContext(t)
	schema := daggerSchemaFixture(t, ctx)
	ctx = dang.ContextWithImportConfigs(ctx, dang.ImportConfig{
		Name: "Dagger", Schema: schema, Client: schemaCacheClient("dagger"), AutoImport: true,
	})
	cache, err := dagql.EngineCache(ctx)
	require.NoError(t, err)
	baseEntries := cache.EntryStats().CompletedArbitrary
	modKey := preparedDangKey("schema-test", "Counter", "Counter")

	const remounts = 16
	var first *dang.PreparedDirectory
	var firstDir string
	before := liveHeap()
	for index := range remounts {
		mounted := remountedDangDir(t, preparedDangRemountSource)
		program, dir, err := prepareRemounted(ctx, mounted)
		require.NoError(t, err)
		_, err = program.Evaluate(ctx)
		require.NoError(t, err)
		if index == 0 {
			first, firstDir = program, dir
			continue
		}
		require.Same(t, first, program, "remount %d compiled again", index)
		require.Equal(t, firstDir, dir)
	}
	retained := int64(liveHeap()) - int64(before)
	entries := cache.EntryStats().CompletedArbitrary - baseEntries
	kept := keptPreparedDangPrograms(t, ctx, modKey)
	t.Logf("%d remounts of one unchanged module: %d prepared entries holding %d programs, %.2f MiB retained",
		remounts, entries, kept, float64(retained)/(1<<20))
	require.Equal(t, 1, entries, "the session keeps one entry per module")
	require.Equal(t, 1, kept, "every remount uses the one compilation")

	// The source is staged at a path that names the module and its source
	// subpath, the same for every mount.
	rel, err := filepath.Rel(staged.root, firstDir)
	require.NoError(t, err)
	require.Regexp(t, `^counter-[0-9a-f]{32}/\.dagger/modules/counter$`, filepath.ToSlash(rel))
	staged.mu.Lock()
	require.Equal(t, 1, staged.lru.Len(), "identical sources share one staged directory")
	staged.mu.Unlock()

	// Edited source is compiled again, from a directory of its own.
	edited := remountedDangDir(t, strings.Replace(preparedDangRemountSource, `"ok"`, `"edited"`, 1))
	program, dir, err := prepareRemounted(ctx, edited)
	require.NoError(t, err)
	require.NotSame(t, first, program)
	require.NotEqual(t, firstDir, dir)
	require.Equal(t, 2, keptPreparedDangPrograms(t, ctx, modKey))
}

// A compilation reused after the mount it was prepared from is gone reports
// its errors at the staged source, with the source shown.
func TestPreparedDangReportsErrorsAtStagedSource(t *testing.T) {
	useStagedDangDirs(t, maxStagedDangSources)
	ctx := schemaCacheContext(t)
	schema, err := dang.SchemaFromSDL("type Query { value: String! }", "schema.graphqls")
	require.NoError(t, err)
	ctx = dang.ContextWithImportConfigs(ctx, dang.ImportConfig{Name: "Session", Schema: schema, Client: schemaCacheClient("session")})
	const source = "import Session\n\npub result: String! = raise \"staged failure\"\n"

	mounted := remountedDangDir(t, source)
	program, dir, err := prepareRemounted(ctx, mounted)
	require.NoError(t, err)
	// The mount is released.
	require.NoError(t, os.RemoveAll(mounted))
	remounted := remountedDangDir(t, source)
	again, againDir, err := prepareRemounted(ctx, remounted)
	require.NoError(t, err)
	require.Same(t, program, again)
	require.Equal(t, dir, againDir)

	_, err = again.Evaluate(ctx)
	require.Error(t, err)
	report := err.Error()
	require.Contains(t, report, filepath.Join(dir, "main.dang")+":3:")
	require.Contains(t, report, `pub result: String! = raise "staged failure"`, "the staged file shows the failing line")
	require.NotContains(t, report, mounted)
}

// Staged directories nobody uses are removed beyond the bound, least recently
// used first; one in use is kept, and one removed is staged again by its next
// use.
func TestStagedDangDirsBoundAndRestage(t *testing.T) {
	staged := useStagedDangDirs(t, 2)
	stage := func(content string) (string, func()) {
		t.Helper()
		source, ok := stageDangSource(t.Context(), remountedDangDir(t, content), "counter", ".dagger/modules/counter")
		require.True(t, ok)
		return source.dir, source.release
	}
	exists := func(dir string) bool {
		_, err := os.Stat(filepath.Join(dir, "main.dang"))
		return err == nil
	}
	a, releaseA := stage("pub a: Int! = 1\n")
	b, releaseB := stage("pub b: Int! = 1\n")
	releaseB()
	c, releaseC := stage("pub c: Int! = 1\n")
	releaseC()
	require.True(t, exists(a), "in use, though least recently used")
	require.False(t, exists(b), "the least recently used unused directory is removed")
	require.True(t, exists(c))
	releaseA()
	releaseA() // a second release is a no-op

	// A directory removed by someone else is staged again.
	require.NoError(t, os.RemoveAll(c))
	again, releaseAgain := stage("pub c: Int! = 1\n")
	defer releaseAgain()
	require.Equal(t, c, again)
	require.True(t, exists(c))

	// A directory left from an earlier engine is replaced, not trusted.
	stale := newStagedDangDirs(staged.root, 2)
	require.NoError(t, os.WriteFile(filepath.Join(a, "main.dang"), []byte("stale"), 0o600))
	saved := stagedDangSources
	stagedDangSources = stale
	defer func() { stagedDangSources = saved }()
	restaged, release := stage("pub a: Int! = 1\n")
	defer release()
	require.Equal(t, a, restaged)
	content, err := os.ReadFile(filepath.Join(a, "main.dang"))
	require.NoError(t, err)
	require.Equal(t, "pub a: Int! = 1\n", string(content))
}

// The staged source is compiled at the Dang version the directory it was
// staged from decides, though no dang.toml governs the staged directory.
func TestStagedDangSourceKeepsVersion(t *testing.T) {
	useStagedDangDirs(t, maxStagedDangSources)
	srcDir := remountedDangDir(t, "pub x: Int! = 1\n")
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "dang.toml"), []byte("dang = \"2.0\"\n"), 0o600))
	source, ok := stageDangSource(t.Context(), srcDir, "counter", ".dagger/modules/counter")
	require.True(t, ok)
	defer source.release()
	require.Equal(t, "2.0", source.version.String())
	_, err := os.Stat(filepath.Join(source.dir, "dang.toml"))
	require.True(t, os.IsNotExist(err), "only .dang files are staged")
	configPath, _, err := dang.FindProjectConfig(source.dir)
	require.NoError(t, err)
	require.Empty(t, configPath)

	other, ok := stageDangSource(t.Context(), remountedDangDir(t, "pub x: Int! = 1\n"), "counter", ".dagger/modules/counter")
	require.True(t, ok)
	defer other.release()
	require.Equal(t, source.dir, other.dir, "the files decide the directory")
	require.NotEqual(t,
		preparedDangCompilationKey(source, "v1.0.0", "schema"),
		preparedDangCompilationKey(other, "v1.0.0", "schema"),
		"the version decides the compilation")
}
