package core

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql/cachefact"
)

// An engine given DAGGER_CLOUD_URL and DAGGER_CLOUD_TOKEN exports its dagql
// cache facts to Dagger Cloud itself, under its own token and as one writer:
// every fact of the instance arrives, in one dense sequence from engine.start
// to engine.stop, even though the fake cloud refuses the first fact export
// once and the exporter has to retry it. The client has no Cloud
// configuration, so every fact record comes from the engine.
func (ClientSuite) TestEngineCacheFactsToCloud(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	thisRepoPath, err := filepath.Abs("../..")
	require.NoError(t, err)
	code := c.Host().Directory(thisRepoPath, dagger.HostDirectoryOpts{
		Include: []string{
			"core/integration/testdata/telemetry/",
			"go.mod",
			"go.sum",
		},
	})
	eventsVol := c.CacheVolume("dagger-cache-facts-events-" + identity.NewID())
	eventsID := identity.NewID()
	base := c.Container().
		From(golangImage).
		With(goCache(c)).
		WithMountedDirectory("/src", code).
		WithWorkdir("/src").
		WithMountedCache("/events", eventsVol)
	fakeCloud := base.
		WithDefaultArgs([]string{"go", "run", "./core/integration/testdata/telemetry/"}).
		WithExposedPort(8080).
		AsService()

	devEngine := devEngineContainerAsService(devEngineContainer(c, func(ctr *dagger.Container) *dagger.Container {
		return ctr.
			WithServiceBinding("cloud", fakeCloud).
			WithEnvVariable("DAGGER_CLOUD_URL", "http://cloud:8080/"+eventsID).
			WithEnvVariable("DAGGER_CLOUD_TOKEN", "test")
	}))
	devEngine, err = devEngine.Start(ctx)
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
