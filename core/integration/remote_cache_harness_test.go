package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// fixtureEngine is one nested dev engine with the gated remote-cache fixture
// enabled on its own fixture volume. Each scenario gets new state keys,
// fixture volumes and clients; cache state volumes never move or share
// storage, and files move between fixture volumes only through the outer
// harness.
type fixtureEngine struct {
	ctx     context.Context
	t       *testctx.T
	outer   *dagger.Client
	name    string
	state   string
	volume  *dagger.CacheVolume
	workdir string
	gated   bool

	upstream, tunnel *dagger.Service
	endpoint         string
	client           *dagger.Client
	unwatch          func()
}

const fixtureEngineRoot = "/transfer-fixture"

// newFixtureEngine starts an engine. gated false starts it without the
// fixture's environment variable, for the absent-gate control.
func newFixtureEngine(ctx context.Context, t *testctx.T, outer *dagger.Client, name string, gated bool) *fixtureEngine {
	t.Helper()
	e := &fixtureEngine{
		ctx:     ctx,
		t:       t,
		outer:   outer,
		name:    name,
		state:   "b7-" + name + "-state-" + identity.NewID(),
		volume:  outer.CacheVolume("b7-" + name + "-fixture-" + identity.NewID()),
		workdir: t.TempDir(),
		gated:   gated,
	}
	// Registered before the first fallible startup step, so a failed start
	// still stops whatever it had started.
	t.Cleanup(func() {
		if err := e.shutdown(); err != nil {
			t.Errorf("stop fixture engine %s: %v", e.name, err)
		}
	})
	e.start()
	return e
}

func (e *fixtureEngine) start() {
	e.t.Helper()
	ctr := devEngineContainerWithStateKey(e.outer, e.state, func(ctr *dagger.Container) *dagger.Container {
		ctr = ctr.WithMountedCache(fixtureEngineRoot, e.volume)
		if e.gated {
			ctr = ctr.WithEnvVariable("_DAGGER_TEST_REMOTE_CACHE_FIXTURE_ROOT", fixtureEngineRoot)
		}
		return ctr
	})
	// Roomy GC bounds isolate ownership from automatic disk-pressure pruning.
	ctr = engineWithConfig(e.ctx, e.t, engineConfigWithEnabled(true), engineConfigWithGC("1000000000000000", "0", "1000000000000000", "0"))(ctr)
	e.upstream = devEngineContainerAsService(ctr)
	e.unwatch = watchNestedEngine(e.t, e.outer, e.upstream, e.t.Name()+" "+e.name)
	tunnel, err := e.outer.Host().Tunnel(e.upstream).Start(e.ctx)
	if err != nil {
		e.t.Fatalf("start fixture engine %s: %v\n%s", e.name, err, nestedEngineEarlyExit(e.ctx, ctr))
	}
	e.tunnel = tunnel
	e.endpoint, err = e.tunnel.Endpoint(e.ctx, dagger.ServiceEndpointOpts{Scheme: "tcp"})
	require.NoError(e.t, err)
	e.client = e.connect()
}

// nestedEngineEarlyExit diagnoses a nested engine whose service exited before
// it served: a service's output is not in its start error. It runs the same
// container once more as a plain exec on the same state, bounded, and returns
// what the engine wrote and how it ended. An engine that is still up after the
// bound is reported as such: its first exit was not repeatable.
func nestedEngineEarlyExit(ctx context.Context, ctr *dagger.Container) string {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 90*time.Second)
	defer cancel()
	entrypoint, err := ctr.Entrypoint(ctx)
	if err != nil {
		return "nested engine early exit: entrypoint: " + err.Error()
	}
	args, err := ctr.DefaultArgs(ctx)
	if err != nil {
		return "nested engine early exit: default args: " + err.Error()
	}
	cmd := append([]string{"sh", "-c", `timeout 30 "$@" 2>&1; echo "nested engine ended with exit $? (124 means it was still running after 30 s)"`, "sh"}, entrypoint...)
	cmd = append(cmd, args...)
	out, err := ctr.
		WithEnvVariable("_DAGGER_TEST_EARLY_EXIT_PROBE", identity.NewID()).
		WithExec(cmd, dagger.ContainerWithExecOpts{InsecureRootCapabilities: true, Expect: dagger.ReturnTypeAny}).
		Stdout(ctx)
	if err != nil {
		return "nested engine early exit: probe: " + err.Error()
	}
	const keep = 16 << 10
	if len(out) > keep {
		out = "…" + out[len(out)-keep:]
	}
	return "nested engine output on a second start on the same state:\n" + out
}

// connect opens one more client session on the running engine.
func (e *fixtureEngine) connect() *dagger.Client {
	e.t.Helper()
	client, err := dagger.Connect(e.ctx, dagger.WithRunnerHost(e.endpoint), dagger.WithWorkdir(e.workdir), dagger.WithLogOutput(testutil.NewTWriter(e.t)))
	require.NoError(e.t, err)
	return client
}

// reconnect closes the engine's client session, ending everything that
// session owned, and opens a fresh one that has loaded nothing.
func (e *fixtureEngine) reconnect() {
	e.t.Helper()
	client := e.client
	e.client = nil
	require.NoError(e.t, closeClientBounded(e.ctx, client))
	e.client = e.connect()
}

// closeClientBounded joins a client's Close, which takes no context, under a
// fresh deadline that does not inherit the test's cancellation.
func closeClientBounded(ctx context.Context, client *dagger.Client) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), fixtureEngineStopTimeout)
	defer cancel()
	closed := make(chan error, 1)
	go func() { closed <- client.Close() }()
	select {
	case err := <-closed:
		return err
	case <-ctx.Done():
		return fmt.Errorf("client close did not return: %w", context.Cause(ctx))
	}
}

// joinBounded waits for one result of a demand started on its own goroutine.
// A demand that never returns fails here, not at the native test timeout.
func joinBounded(t *testctx.T, done <-chan error, what string) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(3 * time.Minute):
		t.Fatalf("%s never returned", what)
		return nil
	}
}

// fixtureEngineStopTimeout bounds each step of a shutdown. A step never
// inherits the test's context, which is usually already canceled when
// cleanup runs, nor the remains of an earlier step's deadline. A clean stop
// of a nested engine writes its checkpoint; with the whole native set running
// on a host that other engines were also loading (load average 60 on 16
// CPUs) ten of some 120 stops took longer than 45 seconds, so the bound is two
// minutes. It is a bound on a failure, not a wait.
const fixtureEngineStopTimeout = 2 * time.Minute

// shutdown is idempotent. Every step gets its own fresh deadline, so one that
// times out cannot hand the next an already-canceled context, and every
// failure is reported. A handle is forgotten only once its step succeeded, so
// the registered cleanup tries again whatever a failed stop left behind.
func (e *fixtureEngine) shutdown() error {
	step := func(run func(ctx context.Context) error) error {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(e.ctx), fixtureEngineStopTimeout)
		defer cancel()
		return run(ctx)
	}
	var errs error
	if client := e.client; client != nil {
		err := closeClientBounded(e.ctx, client)
		// A client is closed at most once, whatever it answered.
		e.client = nil
		errs = errors.Join(errs, err)
	}
	if upstream := e.upstream; upstream != nil {
		err := step(func(ctx context.Context) error {
			_, err := upstream.Stop(ctx)
			return err
		})
		if err == nil {
			e.unwatch()
			e.upstream = nil
		}
		errs = errors.Join(errs, err)
	}
	if tunnel := e.tunnel; tunnel != nil {
		err := step(func(ctx context.Context) error {
			_, err := tunnel.Stop(ctx, dagger.ServiceStopOpts{Kill: true})
			return err
		})
		if err == nil {
			e.tunnel = nil
		}
		errs = errors.Join(errs, err)
	}
	return errs
}

// stopNestedEngine is the same bounded shutdown for the older tests that
// keep their own engine records: each step under its own fresh deadline,
// every step attempted whatever an earlier one answered, errors joined, and a
// handle forgotten only once its step succeeded so a later cleanup can retry
// it. nil handles are skipped.
func stopNestedEngine(ctx context.Context, client **dagger.Client, unwatch func(), upstream, tunnel **dagger.Service) error {
	step := func(run func(ctx context.Context) error) error {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), fixtureEngineStopTimeout)
		defer cancel()
		return run(ctx)
	}
	var errs error
	if client != nil && *client != nil {
		c := *client
		*client = nil
		errs = errors.Join(errs, closeClientBounded(ctx, c))
	}
	if upstream != nil && *upstream != nil {
		svc := *upstream
		err := step(func(ctx context.Context) error {
			_, err := svc.Stop(ctx)
			return err
		})
		if err == nil {
			if unwatch != nil {
				unwatch()
			}
			*upstream = nil
		}
		errs = errors.Join(errs, err)
	}
	if tunnel != nil && *tunnel != nil {
		svc := *tunnel
		err := step(func(ctx context.Context) error {
			_, err := svc.Stop(ctx, dagger.ServiceStopOpts{Kill: true})
			return err
		})
		if err == nil {
			*tunnel = nil
		}
		errs = errors.Join(errs, err)
	}
	return errs
}

// stop is a clean stop that the scenario depends on: a restart or an offline
// edit of saved state must not proceed after a failed one.
func (e *fixtureEngine) stop() {
	e.t.Helper()
	require.NoError(e.t, e.shutdown())
}

// restart stops the engine cleanly and starts it again on the same state and
// fixture volumes.
func (e *fixtureEngine) restart() {
	e.t.Helper()
	e.stop()
	e.start()
}

// fixture calls one operation of the gated field.
func (e *fixtureEngine) fixture(op, path string, ids []string, out any) error {
	if ids == nil {
		ids = []string{}
	}
	if out == nil {
		out = new(json.RawMessage)
	}
	return transferFixture(e.ctx, e.client, op, path, ids, out)
}

// volumeExec runs a shell script in an outer container with this engine's
// fixture volume at /fixture. Files move between fixture volumes only this
// way, after the producing operation acknowledged completion.
func (e *fixtureEngine) volumeExec(script string, env map[string]string, mounts map[string]*dagger.CacheVolume) string {
	e.t.Helper()
	ctr := e.outer.Container().From(alpineImage).
		WithMountedCache("/fixture", e.volume).
		WithEnvVariable("CACHEBUST", identity.NewID())
	for path, volume := range mounts {
		ctr = ctr.WithMountedCache(path, volume)
	}
	for key, value := range env {
		ctr = ctr.WithEnvVariable(key, value)
	}
	out, err := ctr.WithExec([]string{"sh", "-ec", script}).Stdout(e.ctx)
	require.NoError(e.t, err)
	return out
}

// stateExec runs a shell script in an outer container with this engine's
// state volume at /state. The engine must be stopped: cache state volumes
// never share storage with a running engine. It is for damaging saved state
// on purpose, never for moving state between engines.
func (e *fixtureEngine) stateExec(packages []string, script string, env map[string]string) string {
	e.t.Helper()
	require.Nil(e.t, e.upstream, "the engine must be stopped before its state volume is opened")
	ctr := e.outer.Container().From(alpineImage)
	if len(packages) > 0 {
		ctr = ctr.WithExec(append([]string{"apk", "add", "--no-cache"}, packages...))
	}
	ctr = ctr.WithMountedCache("/state", e.outer.CacheVolume(e.state)).WithEnvVariable("CACHEBUST", identity.NewID())
	for key, value := range env {
		ctr = ctr.WithEnvVariable(key, value)
	}
	out, err := ctr.WithExec([]string{"sh", "-ec", script}).Stdout(e.ctx)
	require.NoError(e.t, err)
	return out
}

// control writes one request record under the fixture root's control/
// directory and returns its name.
func (e *fixtureEngine) control(name string, value any) string {
	e.t.Helper()
	raw, err := json.Marshal(value)
	require.NoError(e.t, err)
	e.writeFile(filepath.Join("control", name), string(raw))
	return name
}

// writeFile writes one contained file into the fixture volume.
func (e *fixtureEngine) writeFile(path, content string) {
	e.t.Helper()
	e.volumeExec(`mkdir -p "$(dirname "/fixture/$NAME")"; printf '%s' "$CONTENT" > "/fixture/$NAME"`, map[string]string{"NAME": path, "CONTENT": content}, nil)
}

// editBundle rewrites one bundle in the fixture volume. It is how a scenario
// gives an exported offer addresses, an expiry or a renewal key before the
// import, as an integration would; layers and references are never edited.
// Numbers stay json.Number, so 64-bit IDs survive the round trip.
func (e *fixtureEngine) editBundle(name string, edit func(bundle map[string]any)) {
	e.t.Helper()
	bundle := e.readBundle(name)
	edit(bundle)
	out, err := json.Marshal(bundle)
	require.NoError(e.t, err)
	e.writeFile(filepath.Join("bundles", name), string(out))
}

// readBundle decodes one bundle from the fixture volume.
func (e *fixtureEngine) readBundle(name string) map[string]any {
	e.t.Helper()
	raw := e.volumeExec(`cat "/fixture/bundles/$NAME"`, map[string]string{"NAME": name}, nil)
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var bundle map[string]any
	require.NoError(e.t, decoder.Decode(&bundle))
	return bundle
}

// bundleOffers turns a bundle's selected outputs into offer records, as an
// integration that learned of the chains later would present them.
func (e *fixtureEngine) bundleOffers(name string) []any {
	e.t.Helper()
	var offers []any
	for _, output := range e.readBundle(name)["outputs"].([]any) {
		entry := output.(map[string]any)
		if entry["chain"] == nil {
			continue
		}
		offer := map[string]any{"address": entry["address"], "value": entry["value"], "chain": entry["chain"]}
		if owner, ok := entry["owner"]; ok {
			offer["owner"] = owner
		}
		offers = append(offers, offer)
	}
	require.NotEmpty(e.t, offers)
	return offers
}

// copyFixtureTo copies bundles and blobs to another engine's fixture volume.
func (e *fixtureEngine) copyFixtureTo(other *fixtureEngine, bundles ...string) {
	e.t.Helper()
	script := "mkdir -p /destination/bundles /destination/blobs; [ ! -d /fixture/blobs ] || cp -a /fixture/blobs/. /destination/blobs/"
	for _, bundle := range bundles {
		script += `; cp "/fixture/bundles/` + bundle + `" /destination/bundles/`
	}
	e.volumeExec(script, nil, map[string]*dagger.CacheVolume{"/destination": other.volume})
}

// hostFile writes one file into the engine client's working directory.
func (e *fixtureEngine) hostFile(path, content string) {
	e.t.Helper()
	full := filepath.Join(e.workdir, path)
	require.NoError(e.t, os.MkdirAll(filepath.Dir(full), 0755))
	require.NoError(e.t, os.WriteFile(full, []byte(content), 0644))
}

// fixtureBarrierWaitTimeout bounds one wait for an armed barrier. It is a
// bound on a failure, not a wait: a reached barrier returns at once.
const fixtureBarrierWaitTimeout = 2 * time.Minute

// armedBarrier is one barrier this engine armed: the key the request named
// and the generation the engine assigned, which is what a wait and a release
// name. Its token record is written once, at arming.
type armedBarrier struct {
	dagql.FixtureBarrierArmed
	e     *fixtureEngine
	token string
}

// armBarrier arms one barrier on this engine and keeps its token.
func (e *fixtureEngine) armBarrier(req dagql.FixtureBarrierRequest) *armedBarrier {
	e.t.Helper()
	var armed dagql.FixtureBarrierArmed
	require.NoError(e.t, e.fixture("barrierArm", e.control(req.Key+".json", req), nil, &armed))
	token := e.control(req.Key+"-wait.json", map[string]any{"key": armed.Key, "generation": armed.Generation})
	return &armedBarrier{FixtureBarrierArmed: armed, e: e, token: token}
}

// await waits, within fixtureBarrierWaitTimeout, until the armed occurrence
// is reached, and returns its observation.
func (b *armedBarrier) await(ctx context.Context, t *testctx.T) dagql.FixtureBarrierReached {
	t.Helper()
	waitCtx, cancel := context.WithTimeout(ctx, fixtureBarrierWaitTimeout)
	defer cancel()
	var reached dagql.FixtureBarrierReached
	require.NoError(t, transferFixture(waitCtx, b.e.client, "barrierWait", b.token, []string{}, &reached), "barrier %s was never reached", b.Key)
	return reached
}

// release wakes the armed occurrence. It never inherits a canceled context,
// so a cleanup can release what a failed test left paused; releasing twice
// is harmless. An engine whose client is already closed has nothing to
// release.
func (b *armedBarrier) release() error {
	if b.e.client == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(b.e.ctx), 30*time.Second)
	defer cancel()
	return transferFixture(ctx, b.e.client, "barrierRelease", b.token, []string{}, new(json.RawMessage))
}

// releaseAtCleanup releases the barrier when the test ends, whatever it did,
// so a failed assertion between arming and releasing ends the held operation
// locally rather than at engine shutdown. It is registered after the
// engine's own cleanup and so runs before it.
func (b *armedBarrier) releaseAtCleanup(t *testctx.T) {
	t.Cleanup(func() { _ = b.release() })
}

// partEvents returns the row's part events of one kind.
func partEventsOf(report transferFixtureReport, rowID uint64, kind string) []int {
	var indexes []int
	for i, event := range report.Parts {
		if event.ResultID == rowID && event.Kind == kind {
			indexes = append(indexes, i)
		}
	}
	return indexes
}

// partKindsOf returns the row's part event kinds in order, for failure logs.
func partKindsOf(report transferFixtureReport, rowID uint64) []string {
	var kinds []string
	for _, event := range report.Parts {
		if event.ResultID == rowID {
			kinds = append(kinds, event.Kind)
		}
	}
	return kinds
}

// reachedOf returns the row's journalled points in order, for failure logs.
func reachedOf(report fixtureControlsReport, rowID uint64) []string {
	var points []string
	for _, o := range report.Reached {
		if o.ResultID == rowID {
			points = append(points, string(o.Point)+"("+o.Detail+")")
		}
	}
	return points
}
