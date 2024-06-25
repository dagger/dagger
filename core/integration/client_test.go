package core

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"dagger.io/dagger"
	"github.com/koron-go/prefixw"
	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/dagger/testctx"
)

type ClientSuite struct{}

func TestClient(t *testing.T) {
	testctx.Run(testCtx, t, ClientSuite{}, Middleware()...)
}

func (ClientSuite) TestClose(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	err := c.Close()
	require.NoError(t, err)
}

func (ClientSuite) TestMultiSameTrace(ctx context.Context, t *testctx.T) {
	rootCtx, span := Tracer().Start(ctx, "root")
	defer span.End()

	newClient := func(ctx context.Context, name string) (*dagger.Client, *safeBuffer) {
		out := new(safeBuffer)
		c, err := dagger.Connect(ctx,
			dagger.WithLogOutput(io.MultiWriter(prefixw.New(testutil.NewTWriter(t.T), name+": "), out)))
		require.NoError(t, err)
		t.Cleanup(func() { c.Close() })
		return c, out
	}

	ctx1, span := Tracer().Start(rootCtx, "client 1")
	defer span.End()
	c1, out1 := newClient(ctx1, "client 1")

	// NOTE: the failure mode for these tests is to hang forever, so we'll set a
	// reasonable timeout
	const timeout = 60 * time.Second

	// try to insulate from network flakiness by resolving and using a fully
	// qualified ref beforehand.
	fqRef, err := c1.Container().From(alpineImage).ImageRef(ctx1)
	require.NoError(t, err)

	echo := func(ctx context.Context, c *dagger.Client, msg string) {
		_, err := c.Container().
			From(fqRef).
			// FIXME: have to echo first, then wait, then echo again, because we only
			// wait for logs once we see them the first time, and we only show spans
			// that are slow enough. this could be made more foolproof by adding a
			// span attribute like "hey wait until you see EOF for my logs on these
			// streams" but we don't control the span.
			// NOTE: have to echo slowly enough that the frontend doesn't consider it
			// "boring"
			WithExec([]string{"sh", "-c", "echo hey; sleep 0.5; echo echoed: $0", msg}).Sync(ctx)
		require.NoError(t, err)
	}

	c1msg := identity.NewID()
	echo(ctx1, c1, c1msg)
	require.Eventually(t, func() bool {
		return strings.Contains(out1.String(), "echoed: "+c1msg)
	}, timeout, 10*time.Millisecond)

	ctx2, span := Tracer().Start(rootCtx, "client 2")
	defer span.End()

	// the timeout has to be established before connecting, so we apply it to c2
	// and make sure we close c2 first.
	timeoutCtx2, cancelTimeout := context.WithTimeout(ctx2, timeout)
	defer cancelTimeout()
	c2, out2 := newClient(timeoutCtx2, "client 2")

	c2msg := identity.NewID()
	echo(ctx2, c2, c2msg)
	require.Eventually(t, func() bool {
		return strings.Contains(out2.String(), "echoed: "+c2msg)
	}, timeout, 10*time.Millisecond)

	ctx3, span := Tracer().Start(rootCtx, "client 3")
	defer span.End()
	timeoutCtx3, cancelTimeout := context.WithTimeout(ctx3, timeout)
	defer cancelTimeout()
	c3, out3 := newClient(timeoutCtx3, "client 3")

	c3msg := identity.NewID()
	echo(ctx3, c3, c3msg)
	require.Eventually(t, func() bool {
		return strings.Contains(out3.String(), "echoed: "+c3msg)
	}, timeout, 10*time.Millisecond)

	t.Logf("closing c2 (which has timeout)")
	require.NoError(t, c2.Close())

	t.Logf("closing c3 (which has timeout)")
	require.NoError(t, c3.Close())

	t.Logf("closing c1")
	require.NoError(t, c1.Close())

	t.Logf("asserting")
	require.Regexp(t, `exec.*echo.*`+c1msg+`.*DONE`, out1.String())
	require.NotContains(t, out1.String(), c2msg)
	require.Regexp(t, `exec.*echo.*`+c2msg+`.*DONE`, out2.String())
	require.Equal(t, 1, strings.Count(out1.String(), "echoed: "+c1msg))
	require.NotContains(t, out2.String(), c1msg)
	require.Equal(t, 1, strings.Count(out2.String(), "echoed: "+c2msg))
	require.Regexp(t, `exec.*echo.*`+c3msg+`.*DONE`, out3.String())
	require.Equal(t, 1, strings.Count(out3.String(), "echoed: "+c3msg))
	require.NotContains(t, out3.String(), c1msg)
}
