package core

import (
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// platformProbeSource reads the engine's default platform inside a function,
// as dagger/go's gomod.hostPlatform does. report returns it in a new module
// object, so that the function call has its own result row to export.
const platformProbeSource = "type PlatformProbe {\n" +
	"  pub hostPlatform: String! {\n" +
	"    `${defaultPlatform}`\n" +
	"  }\n" +
	"\n" +
	"  pub report: Report! {\n" +
	"    Report(hostPlatform)\n" +
	"  }\n" +
	"}\n" +
	"\n" +
	"type Report {\n" +
	"  let platform: String! = \"unset\"\n" +
	"\n" +
	"  new(platform: String!) {\n" +
	"    self.platform = platform\n" +
	"    self\n" +
	"  }\n" +
	"\n" +
	"  pub value: String! {\n" +
	"    platform\n" +
	"  }\n" +
	"}\n"

// The engine's default platform is an ambient input of any function whose body
// reads it. A result one engine computed must not be served to an engine of
// another platform, while an engine of the same platform is served it.
func (RemoteCacheTransferSuite) TestEnginePlatformScopesModuleFunctions(ctx context.Context, t *testctx.T) {
	outer := connect(ctx, t)
	native, err := core.NewQuery(outer).DefaultPlatform(ctx)
	require.NoError(t, err)
	other := "linux/arm64"
	if strings.HasPrefix(string(native), "linux/arm64") {
		other = "linux/amd64"
	}

	checkout := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(checkout, "dagger.json"), []byte(`{"name":"platform-probe","engineVersion":"latest","sdk":{"source":"dang"}}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(checkout, "main.dang"), []byte(platformProbeSource), 0o644))

	type probed struct {
		id, contents, engine string
		imported             bool
	}
	probe := func(t *testctx.T, e *definitionEngine) probed {
		t.Helper()
		client := e.connect(ctx, t, checkout)
		defer func() { require.NoError(t, client.Close()) }()
		require.NoError(t, core.NewQuery(client).ModuleSource(".").AsModule().Serve(ctx))
		var data struct {
			DefaultPlatform string
			PlatformProbe   struct {
				Report struct {
					ID    string
					Value string
				}
			}
		}
		require.NoError(t, client.Do(ctx, &dagger.Request{Query: `{defaultPlatform platformProbe{report{id value}}}`}, &dagger.Response{Data: &data}))
		var report transferFixtureReport
		require.NoError(t, transferFixture(ctx, client, "report", "", []string{data.PlatformProbe.Report.ID}, &report))
		require.Len(t, report.Rows, 1)
		require.NotNil(t, report.Rows[0].Call)
		require.Equal(t, "report", report.Rows[0].Call.Field, "the row is the module function call's own")
		return probed{
			id:       data.PlatformProbe.Report.ID,
			contents: data.PlatformProbe.Report.Value,
			engine:   data.DefaultPlatform,
			imported: report.Rows[0].Imported,
		}
	}
	// importInto gives e the bundle that A exported, as the remote cache
	// would merge A's records into another engine of the organization.
	importInto := func(t *testctx.T, a, e *definitionEngine) {
		t.Helper()
		_, err := core.NewQuery(outer).Container().From(alpineImage).WithMountedCache("/source", a.volume).WithMountedCache("/destination", e.volume).
			WithEnvVariable("COPY", rand.Text()).
			WithExec([]string{"sh", "-ec", "mkdir -p /destination/bundles; cp /source/bundles/platform.json /destination/bundles/platform.json; cp -a /source/blobs /destination/"}).Sync(ctx)
		require.NoError(t, err)
		client := e.connect(ctx, t, checkout)
		defer func() { require.NoError(t, client.Close()) }()
		var imported []transferFixtureMapping
		require.NoError(t, transferFixture(ctx, client, "import", "platform.json", []string{}, &imported))
		require.NotEmpty(t, imported)
	}

	a := startDefinitionEngine(ctx, t, outer, "platform-a")
	computed := probe(t, a)
	require.Equal(t, string(native), computed.engine)
	require.Equal(t, computed.engine, computed.contents)
	require.False(t, computed.imported)
	exporter := a.connect(ctx, t, checkout)
	var exported []transferFixtureMapping
	require.NoError(t, transferFixtureSelected(ctx, exporter, "platform.json", []string{computed.id}, []string{}, &exported))
	require.NotEmpty(t, exported)
	require.NoError(t, exporter.Close())

	t.Run("same platform is served", func(ctx context.Context, t *testctx.T) {
		c := startDefinitionEngine(ctx, t, outer, "platform-c")
		importInto(t, a, c)
		served := probe(t, c)
		require.Equal(t, computed.engine, served.engine)
		require.True(t, served.imported, "an engine of the same platform hits A's result")
		require.Equal(t, computed.contents, served.contents)
	})

	t.Run("other platform is not served", func(ctx context.Context, t *testctx.T) {
		b := startDefinitionEngine(ctx, t, outer, "platform-b", func(ctr *core.Container) *core.Container {
			return ctr.WithEnvVariable("_DAGGER_TEST_REMOTE_CACHE_FIXTURE_DEFAULT_PLATFORM", other)
		})
		importInto(t, a, b)
		got := probe(t, b)
		require.NotEqual(t, computed.engine, got.engine, "B runs with another default platform")
		require.False(t, got.imported, "B was served the result A computed on %s", computed.engine)
		require.Equal(t, got.engine, got.contents)
	})
}
