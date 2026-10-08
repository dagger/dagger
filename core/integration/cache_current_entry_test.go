package core

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"dagger.io/dagger/core"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"dagger.io/dagger"
	bkconfig "github.com/dagger/dagger/internal/buildkit/cmd/buildkitd/config"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/internal/testutil"
)

// Two sessions on one engine run the same withExec and read its stdout at
// once. The image is pinned by digest, so no call of the chain is scoped to a
// session, and each call has one recipe in both. The exec waits on a barrier,
// so neither stdout call can finish before the other has started: the test
// sees both sessions computing the stdout recipe (the in-flight map is per
// session), and only then opens the barrier. The second publication adopts
// the first's entry (one current entry per recipe): the engine ends with one
// entry for the stdout recipe, and both sessions read the same bytes.
func (LocalCacheSuite) TestConcurrentSessionsShareOneEntry(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// A tag is resolved once per session; a digest is not.
	image, err := core.NewQuery(c).Container().From(alpineImage).ImageRef(ctx)
	require.NoError(t, err)

	devEngine := devEngineContainerAsService(devEngineContainer(c,
		engineWithBkConfig(ctx, t, func(_ context.Context, _ *testctx.T, cfg bkconfig.Config) bkconfig.Config {
			cfg.GRPC.DebugAddress = "0.0.0.0:6060"
			return cfg
		}),
	))
	tunnel, err := core.NewQuery(c).Host().Tunnel(devEngine).Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = tunnel.Stop(context.Background())
		_, _ = devEngine.Stop(context.Background())
	})
	endpoint, err := tunnel.Endpoint(ctx, core.ServiceEndpointOpts{Scheme: "tcp"})
	require.NoError(t, err)

	sessions := make([]*dagger.Client, 3)
	for i := range sessions {
		session, err := dagger.Connect(ctx, dagger.WithRunnerHost(endpoint), dagger.WithLogOutput(testutil.NewTWriter(t)))
		require.NoError(t, err)
		t.Cleanup(func() { _ = session.Close() })
		sessions[i] = session
	}
	racing, control := sessions[:2], sessions[2]

	// The exec gives up after a minute, so the test cannot hang on it.
	barrier := "concurrent-sessions-barrier-" + identity.NewID()
	withBarrier := func(session *dagger.Client) *core.Container {
		return core.NewQuery(session).Container().From(image).WithMountedCache("/barrier", core.NewQuery(session).CacheVolume(barrier))
	}
	const script = `for i in $(seq 600); do [ -e /barrier/open ] && break; sleep 0.1; done
[ -e /barrier/open ] || exit 1
head -c 16 /dev/urandom | base64`

	outputs := make([]string, len(racing))
	chains := make(chan struct{}, len(racing))
	startStdout := make(chan struct{})
	startCtx, cancelStart := context.WithCancel(ctx)
	defer cancelStart()
	var eg errgroup.Group
	for i, session := range racing {
		eg.Go(func() error {
			// Publish the chain first: when stdout is asked for, the chain's
			// calls hit, and the only calls left in flight are the stdout
			// calls.
			ctr := withBarrier(session).WithExec([]string{"sh", "-c", script})
			if _, err := ctr.ID(ctx); err != nil {
				return err
			}
			chains <- struct{}{}
			select {
			case <-startStdout:
			case <-startCtx.Done():
				return startCtx.Err()
			}
			out, err := ctr.Stdout(ctx)
			outputs[i] = out
			return err
		})
	}
	for range racing {
		select {
		case <-chains:
		case <-time.After(2 * time.Minute):
			t.Fatal("the sessions did not publish their chains")
		}
	}

	// Neither session evaluates until both chains have been published.
	close(startStdout)

	type snapshot struct {
		OngoingCalls []struct {
			CallKey        string `json:"call_key"`
			ConcurrencyKey string `json:"concurrency_key"`
			Completed      bool   `json:"completed"`
		} `json:"ongoing_calls"`
		Results []struct {
			ID      uint64          `json:"shared_result_id"`
			Recipe  string          `json:"result_call_recipe_digest"`
			Indexed bool            `json:"indexed"`
			Frame   json.RawMessage `json:"result_call"`
		} `json:"results"`
	}
	readSnapshot := func() snapshot {
		raw, err := core.NewQuery(c).Container().From(alpineImage).
			WithServiceBinding("dev-engine", devEngine).
			WithEnvVariable("READ", identity.NewID()).
			WithExec([]string{"wget", "-qO-", "http://dev-engine:6060/debug/dagql/cache"}).
			Stdout(ctx)
		require.NoError(t, err)
		var snap snapshot
		require.NoError(t, json.Unmarshal([]byte(raw), &snap))
		return snap
	}

	// raceKey is the call key two sessions compute at once: their stdout.
	var raceKey string
	for deadline := time.Now().Add(2 * time.Minute); raceKey == ""; {
		require.True(t, time.Now().Before(deadline), "the two sessions were never seen computing the same call")
		sessionsByKey := map[string]map[string]struct{}{}
		for _, call := range readSnapshot().OngoingCalls {
			if call.Completed {
				continue
			}
			if sessionsByKey[call.CallKey] == nil {
				sessionsByKey[call.CallKey] = map[string]struct{}{}
			}
			sessionsByKey[call.CallKey][call.ConcurrencyKey] = struct{}{}
		}
		for key, computing := range sessionsByKey {
			if len(computing) >= 2 {
				raceKey = key
			}
		}
		if raceKey == "" {
			time.Sleep(time.Second)
		}
	}
	_, err = withBarrier(control).
		WithEnvVariable("OPEN", identity.NewID()).
		WithExec([]string{"touch", "/barrier/open"}).
		Sync(ctx)
	require.NoError(t, err)
	require.NoError(t, eg.Wait())

	var stdoutEntries, execEntries []uint64
	for _, row := range readSnapshot().Results {
		var call struct {
			Field string `json:"field"`
		}
		if len(row.Frame) > 0 {
			require.NoError(t, json.Unmarshal(row.Frame, &call))
		}
		switch {
		case call.Field == "stdout":
			stdoutEntries = append(stdoutEntries, row.ID)
			require.Equal(t, raceKey, row.Recipe, "the stdout entry is of the call both sessions computed")
		case call.Field == "withExec" && strings.Contains(string(row.Frame), "urandom"):
			execEntries = append(execEntries, row.ID)
		default:
			continue
		}
		t.Logf("%s entry %d: recipe %s, indexed %t", call.Field, row.ID, row.Recipe, row.Indexed)
	}
	require.NotEmpty(t, outputs[0])
	require.Equal(t, outputs[0], outputs[1], "both sessions read the same bytes")
	require.Len(t, execEntries, 1, "one entry for withExec")
	require.Len(t, stdoutEntries, 1, "one entry for stdout, which both sessions computed")
}
