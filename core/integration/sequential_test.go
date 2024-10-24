package core

import (
	"context"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/testctx"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// This suite contains tests that must run on their own. This comes with a heavy time penalty.
// Add additional tests here only if strictly necessary, typical if verifying some type of isolation.
type SequentialSuite struct{}

func TestSequential(t *testing.T) {
	testctx.Run(
		testCtx,
		t,
		SequentialSuite{},
		// omitting testctx.WithParallel middleware to get the desired sequential behavior
		testctx.WithOTelLogging(Logger()),
		testctx.WithOTelTracing(Tracer()),
	)
}

func (SequentialSuite) TestInsecureRootNetNSIsolation(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	opts := dagger.ContainerWithExecOpts{InsecureRootCapabilities: true}
	baseContainer := c.Container().
		From("alpine:latest").
		WithExec([]string{"apk", "add", "iputils", "iptables"}).
		WithEnvVariable("CACHE_BUST", uuid.NewString())

	listNATRules := func(ctr *dagger.Container) (string, error) {
		return ctr.
			WithExec([]string{"sh", "-c", "iptables -t nat -L -v -n"}, opts).
			Stdout(ctx)
	}

	withRules, err := listNATRules(baseContainer.WithExec([]string{
		"sh", "-c", "iptables -t nat -A PREROUTING -p tcp -j DNAT --to-destination 127.0.0.1",
	}, opts).WithExec([]string{
		"sh", "-c", "iptables -t nat -A POSTROUTING -p tcp -j MASQUERADE",
	}, opts))
	require.NoError(t, err)

	withoutRules, err := listNATRules(baseContainer)
	require.NoError(t, err)

	require.NotEqual(t, withRules, withoutRules)
}
