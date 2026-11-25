package core

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"strings"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"

	bkconfig "github.com/dagger/dagger/internal/buildkit/cmd/buildkitd/config"
)

// clientEngine is a nested dev engine of a generated-client test's own: it
// starts with a cache no other test has used, keeps every row its sessions
// make, and serves its debug handlers, so the test can read which
// generate-client execs its cache holds.
type clientEngine struct {
	service  *core.Service
	endpoint string
}

func startClientEngine(ctx context.Context, t *testctx.T, outer *dagger.Client, name string, withs ...func(*core.Container) *core.Container) *clientEngine {
	t.Helper()
	ctr := devEngineContainerWithStateKey(outer, name+"-state-"+rand.Text(), withs...)
	// The rows must outlive the sessions that made them for the counts the
	// tests read to mean anything, independently of the host's disk
	// pressure.
	ctr = engineWithConfig(ctx, t, engineConfigWithEnabled(true), engineConfigWithGC("1000000000000000", "0", "1000000000000000", "0"))(ctr)
	ctr = engineWithBkConfig(ctx, t, func(_ context.Context, _ *testctx.T, cfg bkconfig.Config) bkconfig.Config {
		cfg.GRPC.DebugAddress = "0.0.0.0:6060"
		return cfg
	})(ctr)
	e := &clientEngine{service: devEngineContainerAsService(ctr)}
	tunnel, err := core.NewQuery(outer).Host().Tunnel(e.service).Start(ctx)
	require.NoError(t, err)
	e.endpoint, err = tunnel.Endpoint(ctx, core.ServiceEndpointOpts{Scheme: "tcp"})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = tunnel.Stop(context.WithoutCancel(ctx), core.ServiceStopOpts{Kill: true})
		_, _ = e.service.Stop(context.WithoutCancel(ctx))
	})
	return e
}

func (e *clientEngine) connect(ctx context.Context, t *testctx.T, dir string) *dagger.Client {
	t.Helper()
	client, err := dagger.Connect(ctx, dagger.WithRunnerHost(e.endpoint), dagger.WithWorkdir(dir), dagger.WithLogOutput(testutil.ClientLogOutput(t)))
	require.NoError(t, err)
	return client
}

// generateClientExecs returns the args of each generate-client exec the
// engine's cache holds, read from its debug handlers' cache snapshot.
func (e *clientEngine) generateClientExecs(ctx context.Context, t *testctx.T, outer *dagger.Client) []string {
	t.Helper()
	raw, err := core.NewQuery(outer).Container().From(alpineImage).
		WithServiceBinding("dev-engine", e.service).
		WithEnvVariable("READ", rand.Text()).
		WithExec([]string{"wget", "-qO-", "http://dev-engine:6060/debug/dagql/cache"}).
		Stdout(ctx)
	require.NoError(t, err)
	var snapshot struct {
		Results []struct {
			ID    uint64          `json:"shared_result_id"`
			Frame json.RawMessage `json:"result_call"`
		} `json:"results"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &snapshot))
	var execs []string
	for _, row := range snapshot.Results {
		if len(row.Frame) == 0 {
			continue
		}
		var call struct {
			Field string          `json:"field"`
			Args  json.RawMessage `json:"args"`
		}
		require.NoError(t, json.Unmarshal(row.Frame, &call))
		if call.Field == "withExec" && strings.Contains(string(call.Args), `"generate-client"`) {
			t.Logf("generate-client exec row %d args=%s", row.ID, call.Args)
			execs = append(execs, string(call.Args))
		}
	}
	return execs
}
