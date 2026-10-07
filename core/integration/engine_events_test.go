package core

// These tests cover what an engine reports to Dagger Cloud about its dagql
// cache: its own cache events (dagger.io/engine.cache), and the cache state
// on the session spans that name cache entries, which the events' cache
// identity and result numbers join.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"dagger.io/dagger/core"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine/config"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

// engineEventLine is one engine cache event as the fake Cloud recorded it.
type engineEventLine struct {
	Export       string            `json:"export"`
	Resource     map[string]string `json:"resource"`
	Attrs        map[string]string `json:"attrs"`
	TimeUnixNano int64             `json:"timeUnixNano"`
	Body         string            `json:"body"`
}

func (e engineEventLine) kind() string     { return e.Attrs[telemetryattrs.EngineEventAttr] }
func (e engineEventLine) instance() string { return e.Resource["service.instance.id"] }

func (e engineEventLine) writer(t *testctx.T) string {
	writer, _, ok := strings.Cut(e.Export, "/")
	require.True(t, ok, "every event export carries X-Dagger-Export")
	return writer
}

func decodeEngineEventLine[T any](t *testctx.T, e engineEventLine) T {
	var body T
	require.NoError(t, json.Unmarshal([]byte(e.Body), &body))
	return body
}

// engineEventRuns returns the engine cache events the fake Cloud received,
// one list per engine process in the order the processes started, each in
// the order its events happened.
func engineEventRuns(ctx context.Context, t *testctx.T, cloud telemetrySplitCloud) [][]engineEventLine {
	byInstance := map[string][]engineEventLine{}
	for _, event := range readTelemetrySplitLines[engineEventLine](ctx, t, cloud, "v1/logs.json.engine-events") {
		require.NotEmpty(t, event.instance(), "the process resource names the engine instance")
		byInstance[event.instance()] = append(byInstance[event.instance()], event)
	}
	var runs [][]engineEventLine
	for _, run := range byInstance {
		sort.SliceStable(run, func(i, j int) bool { return run[i].TimeUnixNano < run[j].TimeUnixNano })
		runs = append(runs, run)
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i][0].TimeUnixNano < runs[j][0].TimeUnixNano })
	return runs
}

// kinds returns the kinds of a run's events, snapshot-sharing passes aside:
// whether a pipeline shares snapshots is not what these tests cover.
func engineEventKinds(run []engineEventLine) []string {
	var kinds []string
	for _, event := range run {
		if event.kind() != telemetryattrs.EngineEventShare {
			kinds = append(kinds, event.kind())
		}
	}
	return kinds
}

// cacheSpanLine is one exported span as the fake Cloud recorded it, with its
// cache state attributes.
type cacheSpanLine struct {
	Instance string         `json:"instance"`
	Session  string         `json:"session"`
	Cache    string         `json:"cache"`
	TraceID  string         `json:"traceID"`
	SpanID   string         `json:"spanID"`
	Name     string         `json:"name"`
	Attrs    map[string]any `json:"cacheAttrs"`
}

func (s cacheSpanLine) attr(key string) (string, bool) {
	v, ok := s.Attrs[key]
	if !ok {
		return "", false
	}
	return fmt.Sprint(v), true
}

func (s cacheSpanLine) resultID() (uint64, bool) {
	v, ok := s.attr(telemetryattrs.CacheResultIDAttr)
	if !ok {
		return 0, false
	}
	id, err := strconv.ParseUint(v, 10, 64)
	return id, err == nil
}

func (s cacheSpanLine) parts() []string {
	raw, _ := s.Attrs[telemetryattrs.CachePartsAttr].([]any)
	parts := make([]string, 0, len(raw))
	for _, part := range raw {
		parts = append(parts, fmt.Sprint(part))
	}
	return parts
}

// unionParts adds parts to set, which it keeps sorted and without duplicates.
func unionParts(set, parts []string) []string {
	set = append(set, parts...)
	slices.Sort(set)
	return slices.Compact(set)
}

func (s cacheSpanLine) isSessionEnd() bool {
	complete, _ := s.Attrs[telemetryattrs.WcprofSessionCompleteAttr].(bool)
	return complete
}

// requireSessionCacheSpanCounts requires every session that named a cache
// entry to end with a carrier declaring how many of its spans did, and that
// number to be what the fake Cloud received. It returns the sessions.
func requireSessionCacheSpanCounts(t *testctx.T, spans []cacheSpanLine) map[string][]cacheSpanLine {
	sessions := map[string][]cacheSpanLine{}
	for _, span := range spans {
		if span.Session != "" {
			sessions[span.Session] = append(sessions[span.Session], span)
		}
	}
	for session, spans := range sessions {
		cacheSpans := map[string]bool{}
		carriers := map[string]string{}
		for _, span := range spans {
			if _, ok := span.resultID(); ok {
				cacheSpans[span.SpanID] = true
			}
			if span.isSessionEnd() {
				declared, ok := span.attr(telemetryattrs.CacheSessionSpansAttr)
				require.True(t, ok, "session %s's carrier declares its cache spans", session)
				carriers[span.SpanID] = declared
			}
		}
		if len(cacheSpans) == 0 && len(carriers) == 0 {
			continue
		}
		require.Len(t, carriers, 1, "session %s ends with one carrier", session)
		for _, declared := range carriers {
			require.Equal(t, strconv.Itoa(len(cacheSpans)), declared, "session %s declares the cache spans Cloud received", session)
		}
	}
	return sessions
}

// engineEventsEngine is a dev engine with the given state that exports its
// cache events to the fake Cloud under its own token, with no automatic
// garbage collection, so an explicit prune is its only one.
func engineEventsEngine(ctx context.Context, t *testctx.T, c *dagger.Client, cloud telemetrySplitCloud, state string) *core.Service {
	enable := engineWithConfig(ctx, t, engineConfigWithEnabled(false), func(_ context.Context, _ *testctx.T, cfg config.Config) config.Config {
		cfg.Telemetry.EngineEvents = true
		return cfg
	})
	engine, err := devEngineContainerAsService(devEngineContainerWithStateKey(c, state, enable, func(ctr *core.Container) *core.Container {
		return cloud.bind(ctr).WithEnvVariable("DAGGER_CLOUD_TOKEN", "test")
	})).Start(ctx)
	require.NoError(t, err)
	return engine
}

// An engine given telemetry.engineEvents in its engine.json, DAGGER_CLOUD_URL
// and DAGGER_CLOUD_TOKEN reports its cache's events to Dagger Cloud itself,
// under its own token and as one writer per process, even though the fake
// Cloud refuses the first event export once and the exporter has to retry it.
//
// The first process starts on a new cache, runs a pipeline twice, the second
// time from its cache, and stops cleanly. The second restores the cache under
// the same identity, at the next generation, runs the same pipeline from it,
// prunes it explicitly and stops.
// The session spans that name cache entries carry the cache identity and the
// entries' result numbers the events use: the prune drops edges of entries
// the sessions reported retained. The parts the first session's spans
// reported for an entry are its complete parts: a later hit on it reports the
// same, in the same process and after the restart.
func (ClientSuite) TestEngineEventsToCloud(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	cloud := newTelemetrySplitCloud(t, c)
	cli := daggerCliFile(t, c)
	state := "dagger-engine-events-state-" + identity.NewID()
	query := func(engine *core.Service, query string) {
		_, err := telemetrySplitClient(ctx, t, c, cli, engine, cloud).
			WithEnvVariable("CACHEBUSTER", identity.NewID()).
			WithNewFile("/query.graphql", query).
			WithExec([]string{"/bin/dagger", "query", "--doc", "/query.graphql"}, core.ContainerWithExecOpts{DisableDaggerInDagger: true}).
			Sync(ctx)
		require.NoError(t, err)
	}
	pipeline := fmt.Sprintf(`{ container { from(address: %q) { withExec(args: ["sh", "-c", "echo $0 > /marker", %q]) { file(path: "/marker") { contents } } } } }`, alpineImage, identity.NewID())

	engine := engineEventsEngine(ctx, t, c, cloud, state)
	query(engine, pipeline)
	query(engine, pipeline)
	_, err := engine.Stop(ctx)
	require.NoError(t, err)

	engine = engineEventsEngine(ctx, t, c, cloud, state)
	query(engine, pipeline)
	query(engine, `{ engine { localCache { prune } } }`)
	_, err = engine.Stop(ctx)
	require.NoError(t, err)

	_, err = cloud.reader.
		WithEnvVariable("CACHEBUSTER", identity.NewID()).
		WithExec([]string{"test", "-e", fmt.Sprintf("/events/%s/v1/logs.json.engine-events-refused", cloud.eventsID)}).
		Sync(ctx)
	require.NoError(t, err, "the fake Cloud refused one event export")

	runs := engineEventRuns(ctx, t, cloud)
	require.Len(t, runs, 2, "one engine instance per process")
	writers := map[string]bool{}
	for _, run := range runs {
		runWriters := map[string]bool{}
		for _, event := range run {
			runWriters[event.writer(t)] = true
			writers[event.writer(t)] = true
		}
		require.Len(t, runWriters, 1, "each process's events are one writer")
	}
	require.Len(t, writers, 2)

	first, second := runs[0], runs[1]
	require.Equal(t, []string{telemetryattrs.EngineEventStart, telemetryattrs.EngineEventStop}, engineEventKinds(first))
	require.Equal(t, []string{telemetryattrs.EngineEventStart, telemetryattrs.EngineEventPrune, telemetryattrs.EngineEventStop}, engineEventKinds(second))

	firstStart := decodeEngineEventLine[telemetryattrs.EngineStartEvent](t, first[0])
	require.NotEmpty(t, firstStart.EngineVersion)
	require.NotEmpty(t, firstStart.EngineName)
	require.False(t, firstStart.Restored, "the first process starts on a new cache")
	require.Zero(t, firstStart.RestoredEntries)
	require.Empty(t, firstStart.WipedCache)
	firstStop := decodeEngineEventLine[telemetryattrs.EngineStopEvent](t, first[len(first)-1])
	require.True(t, firstStop.Clean)
	require.Positive(t, firstStop.SavedEntries)

	secondStart := decodeEngineEventLine[telemetryattrs.EngineStartEvent](t, second[0])
	require.True(t, secondStart.Restored, "the second process restores the first's cache")
	require.Positive(t, secondStart.RestoredEntries)
	require.LessOrEqual(t, secondStart.RestoredEntries, firstStop.SavedEntries, "type definitions aside, it restores what the first saved")
	require.Empty(t, secondStart.WipedCache)
	require.True(t, decodeEngineEventLine[telemetryattrs.EngineStopEvent](t, second[len(second)-1]).Clean)

	firstCache, secondCache := first[0].Attrs[telemetryattrs.EngineCacheAttr], second[0].Attrs[telemetryattrs.EngineCacheAttr]
	for _, run := range runs {
		for _, event := range run {
			require.Equal(t, run[0].Attrs[telemetryattrs.EngineCacheAttr], event.Attrs[telemetryattrs.EngineCacheAttr], "a process's events name one cache")
		}
	}
	cacheID, generation, ok := strings.Cut(firstCache, "/")
	require.True(t, ok, "the cache attribute is identity/generation: %q", firstCache)
	require.Equal(t, "1", generation)
	require.Equal(t, cacheID+"/2", secondCache, "the same cache at its next generation")

	var prune engineEventLine
	for _, event := range second {
		if event.kind() == telemetryattrs.EngineEventPrune {
			prune = event
		}
	}
	drops := decodeEngineEventLine[telemetryattrs.EnginePruneEvent](t, prune).Drops
	require.NotEmpty(t, drops)
	for _, drop := range drops {
		require.Greater(t, drop.DroppedAtUnixNano, second[0].TimeUnixNano)
		require.Less(t, drop.DroppedAtUnixNano, second[len(second)-1].TimeUnixNano)
	}
	require.Equal(t, drops[len(drops)-1].DroppedAtUnixNano, prune.TimeUnixNano, "the event is timed at its last drop")

	// The session spans that name cache entries name them in these caches.
	spans := readTelemetrySplitLines[cacheSpanLine](ctx, t, cloud, "v1/traces.json.spans")
	instances := map[string]string{firstCache: first[0].instance(), secondCache: second[0].instance()}
	retained := map[uint64]bool{}
	// reported is what the first process's first session, its first pipeline
	// run, reported of each entry's parts: on its spans, and in any
	// snapshot-sharing event of that process. hits is what hits in later
	// sessions reported, by cache and entry.
	var firstSession string
	reported := map[uint64][]string{}
	hits := map[string]map[uint64][]string{}
	for _, span := range spans {
		id, ok := span.resultID()
		if !ok {
			continue
		}
		require.Contains(t, instances, span.Cache, "span %s %q names an entry of one of the engine's caches", span.SpanID, span.Name)
		require.Equal(t, instances[span.Cache], span.Instance, "span %s %q comes from the process of its cache", span.SpanID, span.Name)
		if v, _ := span.attr(telemetryattrs.CacheRetainedAttr); v == "true" {
			retained[id] = true
		}
		if firstSession == "" && span.Cache == firstCache {
			firstSession = span.Session
		}
		if span.Session == firstSession {
			reported[id] = unionParts(reported[id], span.parts())
			continue
		}
		if outcome, _ := span.attr(telemetryattrs.CacheOutcomeAttr); outcome != "hit" {
			continue
		}
		if hits[span.Cache] == nil {
			hits[span.Cache] = map[uint64][]string{}
		}
		hits[span.Cache][id] = unionParts(hits[span.Cache][id], span.parts())
	}
	for _, event := range first {
		if event.kind() != telemetryattrs.EngineEventShare {
			continue
		}
		for _, part := range decodeEngineEventLine[telemetryattrs.EngineShareEvent](t, event).Parts {
			reported[part.ResultID] = unionParts(reported[part.ResultID], []string{part.Part})
		}
	}
	var droppedRetained bool
	for _, drop := range drops {
		droppedRetained = droppedRetained || retained[drop.ResultID]
	}
	require.True(t, droppedRetained, "the prune drops the edge of an entry a session reported retained")

	var compared int
	for id, parts := range reported {
		for _, cache := range []string{firstCache, secondCache} {
			hit, ok := hits[cache][id]
			if !ok {
				continue
			}
			require.Equal(t, hit, parts, "result %d: the parts the first session reported, against a later hit in %s", id, cache)
			if len(parts) > 0 && cache == secondCache {
				compared++
			}
		}
	}
	require.Positive(t, compared, "the restored cache hits an entry the first session reported with parts")

	requireSessionCacheSpanCounts(t, spans)
}

// sharedTraceScript runs four sessions of telemetry-session, in two traces,
// and orders them by what reaches the fake Cloud: a session's end arrives
// after its teardown stamped it and dropped the trace's wcprof count.
//
// Under $SEQUENTIAL, two sessions run one after the other, each ending before
// the next starts. Under $OVERLAPPING, two sessions overlap: both finish their
// work, A closes, and only once A's end has arrived does B close, with no
// further call.
const sharedTraceScript = `set -e
# carriers waits until $1 distinct session ends reached the fake Cloud.
carriers() {
	for _ in $(seq 600); do
		n=$(grep -h '"wcprof.session_complete":true' "$SPANS" 2>/dev/null | grep -o '"spanID":"[0-9a-f]*"' | sort -u | wc -l)
		[ "$n" -ge "$1" ] && return 0
		sleep 0.2
	done
	echo "timed out waiting for $1 session ends" >&2
	return 1
}
export TRACEPARENT=$SEQUENTIAL
/bin/telemetry-session sequential-1-$MARKER
carriers 1
/bin/telemetry-session sequential-2-$MARKER
carriers 2
export TRACEPARENT=$OVERLAPPING
DONE=/tmp/a-worked AWAIT=/tmp/b-worked /bin/telemetry-session overlapping-a-$MARKER &
a=$!
DONE=/tmp/b-worked AWAIT=/tmp/a-closed /bin/telemetry-session overlapping-b-$MARKER &
b=$!
wait $a
carriers 3
touch /tmp/a-closed
wait $b
carriers 4
`

func randomHex(t *testctx.T, n int) string {
	b := make([]byte, n)
	_, err := rand.Read(b)
	require.NoError(t, err)
	return hex.EncodeToString(b)
}

// Sessions that share a trace each end with their own count of cache spans,
// as section 11 of the telemetry design lays out (sharedTraceScript). One
// after the other, each session's wcprof count covers the spans it started,
// and together they cover the trace. Overlapping, A's wcprof count is the
// whole trace's, B's included, as before; B's session end still arrives, with
// its own cache span count and no wcprof count.
func (ClientSuite) TestCacheSpanCountsPerSessionInSharedTrace(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	cloud := newTelemetrySplitCloud(t, c)
	engine, err := devEngineContainerAsService(telemetrySplitEngine(c, devEngineContainer(c), cloud)).Start(ctx)
	require.NoError(t, err)

	thisRepoPath, err := filepath.Abs("../..")
	require.NoError(t, err)
	code := core.NewQuery(c).Host().Directory(thisRepoPath, core.HostDirectoryOpts{
		Include: []string{"core/integration/testdata/telemetry-session/", "sdk/go/", "go.mod", "go.sum"},
	})
	session := core.NewQuery(c).Container().
		From(golangImage).
		With(goCache(c)).
		WithMountedDirectory("/src", code).
		WithWorkdir("/src").
		WithExec([]string{"go", "build", "-o", "/bin/telemetry-session", "./core/integration/testdata/telemetry-session/"}).
		File("/bin/telemetry-session")
	endpoint, err := engine.Endpoint(ctx, core.ServiceEndpointOpts{Port: 1234, Scheme: "tcp"})
	require.NoError(t, err)

	sequentialTrace, overlappingTrace := randomHex(t, 16), randomHex(t, 16)
	_, err = cloud.bind(core.NewQuery(c).Container().From(alpineImage)).
		WithServiceBinding("dev-engine", engine).
		WithMountedFile("/bin/dagger", daggerCliFile(t, c)).
		WithMountedFile("/bin/telemetry-session", session).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", "/bin/dagger").
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpoint).
		WithEnvVariable("DAGGER_CLOUD_TOKEN", "test").
		WithMountedCache("/events", cloud.events).
		WithEnvVariable("SPANS", "/events/"+cloud.eventsID+"/v1/traces.json.spans").
		WithEnvVariable("IMAGE", alpineImage).
		WithEnvVariable("MARKER", identity.NewID()).
		WithEnvVariable("SEQUENTIAL", "00-"+sequentialTrace+"-"+randomHex(t, 8)+"-01").
		WithEnvVariable("OVERLAPPING", "00-"+overlappingTrace+"-"+randomHex(t, 8)+"-01").
		WithExec([]string{"sh", "-c", sharedTraceScript}, core.ContainerWithExecOpts{DisableDaggerInDagger: true}).
		Sync(ctx)
	require.NoError(t, err)

	spans := readTelemetrySplitLines[cacheSpanLine](ctx, t, cloud, "v1/traces.json.spans")
	sessions := requireSessionCacheSpanCounts(t, spans)
	require.Len(t, sessions, 4)
	for session, spans := range sessions {
		traces := map[string]bool{}
		var cacheSpans int
		for _, span := range spans {
			traces[span.TraceID] = true
			if _, ok := span.resultID(); ok {
				cacheSpans++
			}
		}
		require.Len(t, traces, 1, "session %s is in one trace", session)
		require.Positive(t, cacheSpans, "session %s names cache entries", session)
	}

	// Each trace's session ends, in the order they arrived, and the spans the
	// wcprof counter counted.
	ends := map[string][]cacheSpanLine{}
	seen := map[string]bool{}
	counted := map[string]map[string]bool{}
	for _, span := range spans {
		if span.isSessionEnd() && !seen[span.SpanID] {
			seen[span.SpanID] = true
			ends[span.TraceID] = append(ends[span.TraceID], span)
		}
		if marked, _ := span.Attrs[telemetryattrs.WcprofEngineSpanAttr].(bool); marked {
			if counted[span.TraceID] == nil {
				counted[span.TraceID] = map[string]bool{}
			}
			counted[span.TraceID][span.SpanID] = true
		}
	}
	require.Len(t, ends[sequentialTrace], 2)
	require.Len(t, ends[overlappingTrace], 2)
	wcprofCount := func(end cacheSpanLine) (int, bool) {
		v, ok := end.attr(telemetryattrs.WcprofSessionSpanCountAttr)
		if !ok {
			return 0, false
		}
		n, err := strconv.Atoi(v)
		require.NoError(t, err)
		return n, true
	}

	first, ok := wcprofCount(ends[sequentialTrace][0])
	require.True(t, ok)
	second, ok := wcprofCount(ends[sequentialTrace][1])
	require.True(t, ok, "the second session started its own spans after the first ended")
	require.Positive(t, first)
	require.Positive(t, second)
	require.Equal(t, len(counted[sequentialTrace]), first+second, "one after the other, the two counts cover the trace")

	a, ok := wcprofCount(ends[overlappingTrace][0])
	require.True(t, ok)
	require.Equal(t, len(counted[overlappingTrace]), a, "A's wcprof count is the whole trace's")
	_, ok = wcprofCount(ends[overlappingTrace][1])
	require.False(t, ok, "B's session end has no wcprof count")
}

// A client with DAGGER_CLOUD_TOKEN forwards the token into every engine it
// provisions through a container driver, so the token alone must not make an
// engine export its cache events: without _EXPERIMENTAL_DAGGER_ENGINE_EVENTS
// the provisioned engine sends Cloud no event, while the client's session
// telemetry still arrives. The same engine image with the variable set
// exports its events through the same path, which shows the fake Cloud is
// reachable from it.
//
// Drivers do not forward DAGGER_CLOUD_URL, so the engine images carry it to
// point the provisioned engine at the fake Cloud.
func (ProvisionSuite) TestImageDriverEngineEventsNeedEnable(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	cloud := newTelemetrySplitCloud(t, c)
	cloudHost, err := cloud.service.Hostname(ctx)
	require.NoError(t, err)

	dockerc := dockerSetup(ctx, t, c, containerSetupOpts{name: t.Name(), middleware: func(ctr *core.Container) *core.Container {
		return ctr.WithServiceBinding("cloud", cloud.service)
	}})
	dockerc = dockerc.WithMountedFile("/bin/dagger", daggerCliFile(t, c))

	tarPath, ok := os.LookupEnv("_DAGGER_TESTS_ENGINE_TAR")
	if !ok {
		tarPath = "./bin/engine.tar"
	}
	// The provisioned engine gets its own container network, apart from the
	// one this test's containers resolve the fake Cloud on.
	deviceName, cidr := testutil.GetUniqueNestedEngineNetwork()
	entrypoint := fmt.Sprintf("#!/bin/sh\nexec /usr/local/bin/dagger-entrypoint.sh \"$@\" --network-name %s --network-cidr %s\n", deviceName, cidr)

	provision := func(tag string, enable bool) (eventsID string) {
		eventsID = identity.NewID()
		cloudURL := "http://" + cloudHost + ":8080/" + eventsID
		engineImage := core.NewQuery(c).Container().Import(core.NewQuery(c).Host().File(tarPath)).
			WithNewFile("/usr/local/bin/dagger-test-entrypoint.sh", entrypoint, core.ContainerWithNewFileOpts{Permissions: 0o755}).
			WithEntrypoint([]string{"/usr/local/bin/dagger-test-entrypoint.sh"}).
			WithEnvVariable("DAGGER_CLOUD_URL", cloudURL)
		if enable {
			engineImage = engineImage.WithEnvVariable("_EXPERIMENTAL_DAGGER_ENGINE_EVENTS", "1")
		}
		ctr, err := loadEngineTar(ctx, dockerc, "docker", tag, engineImage.AsTarball(core.ContainerAsTarballOpts{
			ForcedCompression: core.ImageLayerCompressionGzip,
			MediaTypes:        core.ImageMediaTypesDockerMediaTypes,
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
			}, core.ContainerWithExecOpts{InsecureRootCapabilities: true, DisableDaggerInDagger: true}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, marker)

		// A graceful stop emits engine.stop and flushes any events.
		_, err = ctr.
			WithEnvVariable("CACHEBUSTER", identity.NewID()).
			WithExec([]string{"sh", "-c", "docker stop -t 60 $(docker ps -q)"}).
			Sync(ctx)
		require.NoError(t, err)
		return eventsID
	}
	// events runs script where the fake Cloud records what it received
	// under eventsID.
	events := func(eventsID, script string) string {
		out, err := cloud.reader.
			WithEnvVariable("CACHEBUSTER", identity.NewID()).
			WithWorkdir("/events/"+eventsID+"/v1").
			WithExec([]string{"sh", "-c", script}, core.ContainerWithExecOpts{
				Expect: core.ReturnTypeAny,
			}).
			Stdout(ctx)
		require.NoError(t, err)
		return strings.TrimSpace(out)
	}

	off := provision("registry.dagger.io/engine:dev-engine-events-off", false)
	require.Equal(t, "yes", events(off, "test -s traces.json && echo yes"), "the client's session telemetry reaches Cloud")
	require.Equal(t, "none", events(off, "test -e logs.json.engine-events || echo none"), "the provisioned engine sent no cache event")

	on := provision("registry.dagger.io/engine:dev-engine-events-on", true)
	require.Equal(t, "yes", events(on, "test -s traces.json && echo yes"))
	sent := events(on, "cat logs.json.engine-events")
	require.Contains(t, sent, telemetryattrs.EngineEventStart)
	require.Contains(t, sent, telemetryattrs.EngineEventStop)
}
