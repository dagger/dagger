package core

import (
	"context"
	_ "embed"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// FutureSuite contains tests for behavior changes that are "scheduled" - that
// gate functionality behind certain future releases.
//
// As those future releases are actually made, tests can be removed from here
type FutureSuite struct{}

func TestFuture(t *testing.T) {
	testctx.Run(testCtx, t, FutureSuite{}, Middleware()...)
}

func futureClient(ctx context.Context, t *testctx.T, futureVersion string) *dagger.Container {
	c := connect(ctx, t)

	devEngine := devEngineContainer(c, func(c *dagger.Container) *dagger.Container {
		return c.WithEnvVariable("_EXPERIMENTAL_DAGGER_VERSION", futureVersion)
	}).AsService()
	devClient, err := engineClientContainer(ctx, t, c, devEngine)
	devClient = devClient.WithEnvVariable("_EXPERIMENTAL_DAGGER_VERSION", futureVersion)
	require.NoError(t, err)

	return devClient.
		WithWorkdir("/work")
}

func (FutureSuite) TestGoScopedEnumValues(ctx context.Context, t *testctx.T) {
	// Introduced in dagger/dagger#8669
	//
	// Ensure that new dagger unscoped enum values are removed.

	c := futureClient(ctx, t, "v0.14.0")
	c = c.
		WithExec([]string{"dagger", "init", "--name=test", "--sdk=go", "--source=."}).
		WithNewFile("dagger.json", `{"name": "test", "sdk": "go", "source": ".", "engineVersion": "v0.14.0"}`).
		WithNewFile("main.go", `package main

import "dagger/test/internal/dagger"

type Test struct {}

func (m *Test) OldProto(proto dagger.NetworkProtocol) dagger.NetworkProtocol {
	switch proto {
	case dagger.Tcp:
		return dagger.Udp
	case dagger.Udp:
		return dagger.Tcp
	default:
		panic("nope")
	}
}

func (m *Test) NewProto(proto dagger.NetworkProtocol) dagger.NetworkProtocol {
	switch proto {
	case dagger.NetworkProtocolTcp:
		return dagger.NetworkProtocolUdp
	case dagger.NetworkProtocolUdp:
		return dagger.NetworkProtocolTcp
	default:
		panic("nope")
	}
}
`,
		)

	out, err := c.
		WithExec([]string{"sh", "-c", "! dagger call old-proto --proto=TCP"}).
		Stderr(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "undefined: dagger.Tcp")
	require.Contains(t, out, "undefined: dagger.Udp")
	require.NotContains(t, out, "undefined: dagger.NetworkProtocolTcp")
	require.NotContains(t, out, "undefined: dagger.NetworkProtocolUdp")
}
