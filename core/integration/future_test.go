package core

import (
	"context"
	_ "embed"
	"testing"

	dagger "github.com/dagger/dagger/internal/testutil/dagger"
	"github.com/dagger/testctx"
)

// FutureSuite contains tests for behavior changes that are "scheduled" - that
// gate functionality behind certain future releases.
//
// As those future releases are actually made, tests can be removed from here
type FutureSuite struct{}

func TestFuture(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(FutureSuite{})
}

//nolint:unused
func futureClient(ctx context.Context, t *testctx.T, futureVersion string) *dagger.Container {
	c := connect(ctx, t)

	devEngine := devEngineContainer(c, func(c *dagger.Container) *dagger.Container {
		return c.WithEnvVariable("_EXPERIMENTAL_DAGGER_VERSION", futureVersion)
	})
	devClient := engineClientContainer(ctx, t, c, devEngineContainerAsService(devEngine))
	devClient = devClient.WithEnvVariable("_EXPERIMENTAL_DAGGER_VERSION", futureVersion)

	return devClient.
		WithWorkdir("/work")
}
