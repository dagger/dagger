package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql/cachefact"
)

// An engine given _EXPERIMENTAL_DAGGER_CACHE_FACTS_EXPORT, DAGGER_CLOUD_URL and
// DAGGER_CLOUD_TOKEN exports its dagql cache facts to Dagger Cloud itself,
// under its own token and as one writer: every fact of the instance arrives,
// in one dense sequence from engine.start to engine.stop, even though the fake
// cloud refuses the first fact export once and the exporter has to retry it.
// The client has no Cloud configuration, so every fact record comes from the
// engine.
func (ClientSuite) TestEngineCacheFactsToCloud(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	code := cacheFactsFakeCloudCode(t, c)
	eventsID := identity.NewID()
	base, fakeCloud := cacheFactsFakeCloud(c, code)

	devEngine := devEngineContainerAsService(devEngineContainer(c, func(ctr *dagger.Container) *dagger.Container {
		return ctr.
			WithServiceBinding("cloud", fakeCloud).
			WithEnvVariable(envCacheFactsExport, "1").
			WithEnvVariable("DAGGER_CLOUD_URL", "http://cloud:8080/"+eventsID).
			WithEnvVariable("DAGGER_CLOUD_TOKEN", "test")
	}))
	devEngine, err := devEngine.Start(ctx)
	require.NoError(t, err)

	marker := identity.NewID()
	out, err := engineClientContainer(ctx, t, c, devEngine).
		WithEnvVariable("CACHEBUSTER", marker).
		WithExec([]string{
			"dagger", "core", "container",
			"from", "--address=" + alpineImage,
			"with-exec", "--args", "echo," + marker,
			"stdout",
		}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, marker)

	// A graceful stop emits engine.stop and flushes the facts.
	_, err = devEngine.Stop(ctx)
	require.NoError(t, err)

	factsPath := fmt.Sprintf("/events/%s/v1/logs.json.facts", eventsID)
	raw, err := base.
		WithEnvVariable("CACHEBUSTER", identity.NewID()).
		WithExec([]string{"test", "-e", fmt.Sprintf("/events/%s/v1/logs.json.facts-refused", eventsID)}).
		WithExec([]string{"cat", factsPath}).
		Stdout(ctx)
	require.NoError(t, err, "the fake cloud refused one fact export and later recorded facts")

	type factLine struct {
		Export   string            `json:"export"`
		Resource map[string]string `json:"resource"`
		Attrs    map[string]string `json:"attrs"`
		Body     string            `json:"body"`
	}
	var (
		facts    []cachefact.Fact
		writers  = map[string]bool{}
		instance string
	)
	for line := range strings.SplitSeq(strings.TrimSpace(raw), "\n") {
		var got factLine
		require.NoError(t, json.Unmarshal([]byte(line), &got))
		writer, _, ok := strings.Cut(got.Export, "/")
		require.True(t, ok, "every fact export carries X-Dagger-Export")
		writers[writer] = true
		if instance == "" {
			instance = got.Resource[cachefact.ResourceEngineInstance]
		}
		require.NotEmpty(t, instance)
		require.Equal(t, instance, got.Resource[cachefact.ResourceEngineInstance], "one engine instance")
		require.Equal(t, cachefact.Version, got.Attrs[cachefact.AttrVersion])
		fact, err := cachefact.Decode([]byte(got.Body))
		require.NoError(t, err)
		require.Equal(t, strconv.FormatUint(fact.Seq, 10), got.Attrs[cachefact.AttrSeq])
		require.Equal(t, string(fact.Kind()), got.Attrs[cachefact.AttrKind])
		facts = append(facts, fact)
	}
	require.Len(t, writers, 1, "the engine's fact exporter is one writer")

	kinds := map[cachefact.Kind]int{}
	seqs := map[uint64]bool{}
	for _, fact := range facts {
		require.False(t, seqs[fact.Seq], "fact %d arrived twice", fact.Seq)
		seqs[fact.Seq] = true
		kinds[fact.Kind()]++
	}
	for seq := uint64(1); seq <= uint64(len(facts)); seq++ {
		require.True(t, seqs[seq], "fact %d of %d is missing", seq, len(facts))
	}
	byKind := func(kind cachefact.Kind) cachefact.Fact {
		for _, fact := range facts {
			if fact.Kind() == kind {
				return fact
			}
		}
		t.Fatalf("no %s fact", kind)
		return cachefact.Fact{}
	}
	start := byKind(cachefact.KindEngineStart)
	require.EqualValues(t, 1, start.Seq, "engine.start is the first fact of a fresh engine")
	require.Equal(t, cachefact.BootFresh, start.Body.(cachefact.EngineStart).Boot)
	stop := byKind(cachefact.KindEngineStop)
	require.EqualValues(t, len(facts), stop.Seq, "engine.stop is the last fact")
	require.True(t, stop.Body.(cachefact.EngineStop).Clean)
	for _, kind := range []cachefact.Kind{cachefact.KindResult, cachefact.KindDeps, cachefact.KindRetention, cachefact.KindRemoved} {
		require.Positive(t, kinds[kind], "the pipeline produced %s facts", kind)
	}
}

// envCacheFactsExport enables an engine's export of its cache facts.
const envCacheFactsExport = "_EXPERIMENTAL_DAGGER_CACHE_FACTS_EXPORT"

func cacheFactsFakeCloudCode(t *testctx.T, c *dagger.Client) *dagger.Directory {
	thisRepoPath, err := filepath.Abs("../..")
	require.NoError(t, err)
	return c.Host().Directory(thisRepoPath, dagger.HostDirectoryOpts{
		Include: []string{
			"core/integration/testdata/telemetry/",
			"go.mod",
			"go.sum",
		},
	})
}

// cacheFactsFakeCloud returns the fake cloud, which records what it receives
// under /events/<id>/ for the URL http://<host>:8080/<id>, and a container
// with the same /events to read it from.
func cacheFactsFakeCloud(c *dagger.Client, code *dagger.Directory) (*dagger.Container, *dagger.Service) {
	base := c.Container().
		From(golangImage).
		With(goCache(c)).
		WithMountedDirectory("/src", code).
		WithWorkdir("/src").
		WithMountedCache("/events", c.CacheVolume("dagger-cache-facts-events-"+identity.NewID()))
	fakeCloud := base.
		WithDefaultArgs([]string{"go", "run", "./core/integration/testdata/telemetry/"}).
		WithExposedPort(8080).
		AsService()
	return base, fakeCloud
}

// A client with DAGGER_CLOUD_TOKEN forwards the token into every engine it
// provisions through a container driver, so the token alone must not make an
// engine export cache facts: without _EXPERIMENTAL_DAGGER_CACHE_FACTS_EXPORT the
// provisioned engine sends Cloud no fact, while the client's session telemetry
// still arrives. The same engine image with the variable set exports its facts
// through the same path, which shows the fake cloud is reachable from it.
//
// Drivers do not forward DAGGER_CLOUD_URL, so the engine images carry it to
// point the provisioned engine at the fake cloud.
func (ProvisionSuite) TestImageDriverCacheFactsNeedEnable(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base, fakeCloud := cacheFactsFakeCloud(c, cacheFactsFakeCloudCode(t, c))
	cloudHost, err := fakeCloud.Hostname(ctx)
	require.NoError(t, err)

	dockerc := dockerSetup(ctx, t, c, containerSetupOpts{name: t.Name(), middleware: func(ctr *dagger.Container) *dagger.Container {
		return ctr.WithServiceBinding("cloud", fakeCloud)
	}})
	dockerc = dockerc.WithMountedFile("/bin/dagger", daggerCliFile(t, c))

	tarPath, ok := os.LookupEnv("_DAGGER_TESTS_ENGINE_TAR")
	if !ok {
		tarPath = "./bin/engine.tar"
	}
	// The provisioned engine gets its own container network, apart from the
	// one this test's containers resolve the fake cloud on.
	deviceName, cidr := testutil.GetUniqueNestedEngineNetwork()
	entrypoint := fmt.Sprintf("#!/bin/sh\nexec /usr/local/bin/dagger-entrypoint.sh \"$@\" --network-name %s --network-cidr %s\n", deviceName, cidr)

	provision := func(tag string, enable bool) (eventsID string) {
		eventsID = identity.NewID()
		cloudURL := "http://" + cloudHost + ":8080/" + eventsID
		engineImage := c.Container().Import(c.Host().File(tarPath)).
			WithNewFile("/usr/local/bin/dagger-test-entrypoint.sh", entrypoint, dagger.ContainerWithNewFileOpts{Permissions: 0o755}).
			WithEntrypoint([]string{"/usr/local/bin/dagger-test-entrypoint.sh"}).
			WithEnvVariable("DAGGER_CLOUD_URL", cloudURL)
		if enable {
			engineImage = engineImage.WithEnvVariable(envCacheFactsExport, "1")
		}
		ctr, err := loadEngineTar(ctx, dockerc, "docker", tag, engineImage.AsTarball(dagger.ContainerAsTarballOpts{
			ForcedCompression: dagger.ImageLayerCompressionGzip,
			MediaTypes:        dagger.ImageMediaTypesDockerMediaTypes,
		}))
		require.NoError(t, err)

		marker := identity.NewID()
		out, err := ctr.
			WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", "image+docker://"+tag).
			WithEnvVariable("DAGGER_CLOUD_TOKEN", "test").
			WithEnvVariable("DAGGER_CLOUD_URL", cloudURL).
			WithExec([]string{
				"dagger", "core", "container",
				"from", "--address=" + alpineImage,
				"with-exec", "--args", "echo," + marker,
				"stdout",
			}, dagger.ContainerWithExecOpts{InsecureRootCapabilities: true}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, marker)

		// A graceful stop emits engine.stop and flushes any facts.
		_, err = ctr.
			WithEnvVariable("CACHEBUSTER", identity.NewID()).
			WithExec([]string{"sh", "-c", "docker stop -t 60 $(docker ps -q)"}).
			Sync(ctx)
		require.NoError(t, err)
		return eventsID
	}
	// events runs script where the fake cloud records what it received
	// under eventsID.
	events := func(eventsID, script string) string {
		out, err := base.
			WithEnvVariable("CACHEBUSTER", identity.NewID()).
			WithWorkdir("/events/"+eventsID+"/v1").
			WithExec([]string{"sh", "-c", script}, dagger.ContainerWithExecOpts{
				Expect: dagger.ReturnTypeAny,
			}).
			Stdout(ctx)
		require.NoError(t, err)
		return strings.TrimSpace(out)
	}

	off := provision("registry.dagger.io/engine:dev-cache-facts-off", false)
	require.Equal(t, "yes", events(off, "test -s traces.json && echo yes"), "the client's session telemetry reaches Cloud")
	require.Equal(t, "none", events(off, "test -e logs.json.facts || echo none"), "the provisioned engine sent no cache fact")

	on := provision("registry.dagger.io/engine:dev-cache-facts-on", true)
	require.Equal(t, "yes", events(on, "test -s traces.json && echo yes"))
	facts := events(on, "cat logs.json.facts")
	require.Contains(t, facts, string(cachefact.KindEngineStart))
	require.Contains(t, facts, string(cachefact.KindEngineStop))
}
