package core

import (
	"context"
	"os"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pinnedDetachWait outlasts core.DetachGracePeriod (10s), after which a
// service no binding holds anymore is stopped.
const pinnedDetachWait = 15 * time.Second

// TestPinnedAcrossSessions covers the intent Service.start records in the
// service it returns: a pinned service's recipe, loaded in a fresh session
// (as a resumed agent's module state would be), is started on first use and
// kept running, rather than started and stopped around every binding. The
// unpinned recipe of the same service keeps today's behaviour, and
// Service.stop returns an unpinned service.
func (ServiceSuite) TestPinnedAcrossSessions(ctx context.Context, t *testctx.T) {
	if _, nested := os.LookupEnv("DAGGER_SESSION_PORT"); nested {
		t.Skip("needs its own CLI session to capture recipes from call payloads")
	}

	nonce := identity.NewID()
	// Every start of the service serves a fresh random token, so equal
	// tokens mean the same running instance.
	tokenService := func(c *dagger.Client) *dagger.Service {
		return c.Container().From(busyboxImage).
			WithEnvVariable("NONCE", nonce).
			WithExposedPort(8080).
			AsService(dagger.ContainerAsServiceOpts{
				Args: []string{"sh", "-c", `mkdir -p /www; od -An -N8 -tx1 /dev/urandom | tr -d ' \n' > /www/index.html; exec httpd -f -p 8080 -h /www`},
			})
	}
	fetch := func(c *dagger.Client, svc *dagger.Service) string {
		out, err := c.Container().From(alpineImage).
			WithServiceBinding("www", svc).
			WithEnvVariable("BUST", identity.NewID()).
			WithExec([]string{"wget", "-qO-", "http://www:8080"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.NotEmpty(t, out)
		return out
	}

	sink := newAgentTraceSink(t)
	c1 := connect(ctx, t, sink.clientOpts()...)
	svc1 := tokenService(c1)
	pinned1, err := svc1.Start(ctx)
	require.NoError(t, err)

	// The pin does not change the service's identity: both values have the
	// same hostname and reach the same running instance.
	hostname, err := svc1.Hostname(ctx)
	require.NoError(t, err)
	pinnedHostname, err := pinned1.Hostname(ctx)
	require.NoError(t, err)
	require.Equal(t, hostname, pinnedHostname)
	require.Equal(t, fetch(c1, svc1), fetch(c1, pinned1))

	// Rebuild the portable recipes of the pinned service and of its unpinned
	// receiver from the call payloads, as a resume would.
	var pinnedRecipe, unpinnedRecipe string
	require.EventuallyWithT(t, func(ct *assert.CollectT) {
		sink.read(func(db *dagui.DB) {
			for dig, call := range db.Calls {
				if call.Field != "__pinned" {
					continue
				}
				id, err := db.CallIDForDigest(dig)
				if !assert.NoError(ct, err) {
					return
				}
				pinnedRecipe, err = id.Encode()
				if !assert.NoError(ct, err) {
					return
				}
				id, err = db.CallIDForDigest(call.ReceiverDigest)
				if !assert.NoError(ct, err) {
					return
				}
				unpinnedRecipe, err = id.Encode()
				assert.NoError(ct, err)
				return
			}
		})
		assert.NotEmpty(ct, pinnedRecipe, "no Service.__pinned call payload")
		assert.NotEmpty(ct, unpinnedRecipe)
	}, time.Minute, 100*time.Millisecond)
	require.NoError(t, c1.Close())

	// Fresh sessions: one holds the pinned recipe, one the unpinned recipe.
	c2 := connect(ctx, t)
	pinned := dagger.Ref[*dagger.Service](c2, dagger.ID(pinnedRecipe))
	c3 := connect(ctx, t)
	unpinned := dagger.Ref[*dagger.Service](c3, dagger.ID(unpinnedRecipe))

	pinnedFirst := fetch(c2, pinned)
	unpinnedFirst := fetch(c3, unpinned)
	time.Sleep(pinnedDetachWait)
	// The pinned service was started by its first binding and kept running.
	require.Equal(t, pinnedFirst, fetch(c2, pinned))
	// The unpinned one stopped after its binding's grace period.
	require.NotEqual(t, unpinnedFirst, fetch(c3, unpinned))

	// Stopping a pinned service stops it and returns it unpinned: binding
	// the result starts a new instance that is stopped after use again.
	stopped, err := pinned.Stop(ctx)
	require.NoError(t, err)
	stoppedFirst := fetch(c2, stopped)
	require.NotEqual(t, pinnedFirst, stoppedFirst)
	time.Sleep(pinnedDetachWait)
	require.NotEqual(t, stoppedFirst, fetch(c2, stopped))
}
