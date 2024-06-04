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
)

func TestClientClose(t *testing.T) {
	t.Parallel()
	c, _ := connect(t)

	err := c.Close()
	require.NoError(t, err)
}

func TestClientMultiSameTrace(t *testing.T) {
	t.Parallel()

	rootCtx, span := Tracer().Start(testCtx, "root")
	defer span.End()

	newClient := func(ctx context.Context, name string) (*dagger.Client, *safeBuffer) {
		out := new(safeBuffer)
		c, err := dagger.Connect(ctx,
			dagger.WithLogOutput(io.MultiWriter(prefixw.New(newTWriter(t), name+": "), out)))
		require.NoError(t, err)
		t.Cleanup(func() { c.Close() })
		return c, out
	}

	ctx1, span := Tracer().Start(rootCtx, "client 1")
	defer span.End()
	c1, out1 := newClient(ctx1, "client 1")

	// NOTE: the failure mode for these tests is to hang forever, so let's set a
	// reasonable timeout and try to insulate from network flakiness by resolving
	// and using the image beforehand.
	//
	// the timeout has to be established before connecting, so we apply it to c2
	// and make sure we close c2 first.
	fqRef, err := c1.Container().From(alpineImage).ImageRef(ctx1)
	require.NoError(t, err)

	echo := func(c *dagger.Client, msg string) {
		_, err := c.Container().
			From(fqRef).
			// NOTE: have to echo slowly enough that the frontend doesn't consider it
			// "boring"
			WithExec([]string{"sh", "-c", "sleep 0.5; echo echoed: $0", msg}).Sync(ctx1)
		require.NoError(t, err)
	}

	c1msg := identity.NewID()
	echo(c1, c1msg)

	ctx2, span := Tracer().Start(rootCtx, "client 2")
	defer span.End()
	timeoutCtx2, cancelTimeout := context.WithTimeout(ctx2, 10*time.Second)
	defer cancelTimeout()
	c2, out2 := newClient(timeoutCtx2, "client 2")

	c2msg := identity.NewID()
	echo(c2, c2msg)

	ctx3, span := Tracer().Start(rootCtx, "client 3")
	defer span.End()
	timeoutCtx3, cancelTimeout := context.WithTimeout(ctx3, 10*time.Second)
	defer cancelTimeout()
	c3, out3 := newClient(timeoutCtx3, "client 3")

	c3msg := identity.NewID()
	echo(c3, c3msg)

	t.Logf("closing c2 (which has timeout)")
	require.NoError(t, c2.Close())

	t.Logf("closing c3 (which has timeout)")
	require.NoError(t, c3.Close())

	t.Logf("closing c1")
	require.NoError(t, c1.Close())

	// FIXME: unfortunately we have to wait a bit because logs are decoupled from
	// spans and there's no EOF event to signify the end of logs. but this will
	// surely flake in CI someday. sorry.
	t.Logf("waiting")
	time.Sleep(time.Second)

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
