package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	endpoint, err := e.tunnel.Endpoint(e.ctx, dagger.ServiceEndpointOpts{Scheme: "tcp"})
	require.NoError(e.t, err)
	e.client, err = dagger.Connect(e.ctx, dagger.WithRunnerHost(endpoint), dagger.WithWorkdir(e.workdir), dagger.WithLogOutput(testutil.NewTWriter(e.t)))
	require.NoError(e.t, err)
}

// fixtureEngineStopTimeout bounds one whole shutdown. It never inherits the
// test's context, which is usually already canceled when cleanup runs.
const fixtureEngineStopTimeout = 90 * time.Second

// shutdown is idempotent. It attempts every step under one fresh deadline and
// reports every failure, so one failed step never strands the rest.
func (e *fixtureEngine) shutdown() error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(e.ctx), fixtureEngineStopTimeout)
	defer cancel()
	var errs error
	if client := e.client; client != nil {
		e.client = nil
		// Close takes no context; join it under the deadline.
		closed := make(chan error, 1)
		go func() { closed <- client.Close() }()
		select {
		case err := <-closed:
			errs = errors.Join(errs, err)
		case <-ctx.Done():
			errs = errors.Join(errs, fmt.Errorf("client close did not return: %w", context.Cause(ctx)))
		}
	}
	if upstream := e.upstream; upstream != nil {
		e.upstream = nil
		e.unwatch()
		_, err := upstream.Stop(ctx)
		errs = errors.Join(errs, err)
	}
	if tunnel := e.tunnel; tunnel != nil {
		e.tunnel = nil
		_, err := tunnel.Stop(ctx, dagger.ServiceStopOpts{Kill: true})
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
