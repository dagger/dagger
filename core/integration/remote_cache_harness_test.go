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
	require.NoError(e.t, err)
	e.tunnel = tunnel
	e.endpoint, err = e.tunnel.Endpoint(e.ctx, dagger.ServiceEndpointOpts{Scheme: "tcp"})
	require.NoError(e.t, err)
	e.client = e.connect()
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
	require.NoError(e.t, e.client.Close())
	e.client = e.connect()
}

// fixtureEngineStopTimeout bounds each step of a shutdown. A step never
// inherits the test's context, which is usually already canceled when
// cleanup runs, nor the remains of an earlier step's deadline.
const fixtureEngineStopTimeout = 45 * time.Second

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
		err := step(func(ctx context.Context) error {
			// Close takes no context; join it under the deadline.
			closed := make(chan error, 1)
			go func() { closed <- client.Close() }()
			select {
			case err := <-closed:
				return err
			case <-ctx.Done():
				return fmt.Errorf("client close did not return: %w", context.Cause(ctx))
			}
		})
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
