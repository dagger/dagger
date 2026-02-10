package core

import (
	"context"
	"strings"

	dagger "github.com/dagger/dagger/internal/testutil/dagger"
	"github.com/dagger/dagger/engine/config"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (EngineSuite) TestRegistryMirrorsCustomCA(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	certGen := newGeneratedCerts(c, "ca")
	registryCert, registryKey := certGen.newServerCerts("testreg")

	cacheVolume := c.CacheVolume(t.Name())
	registry := c.Container().From("registry:3").
		WithFile("/certs/domain.crt", registryCert).
		WithFile("/certs/domain.key", registryKey).
		WithEnvVariable("REGISTRY_HTTP_TLS_CERTIFICATE", "/certs/domain.crt").
		WithEnvVariable("REGISTRY_HTTP_TLS_KEY", "/certs/domain.key").
		WithExposedPort(5000, dagger.ContainerWithExposedPortOpts{Protocol: dagger.NetworkProtocolTcp}).
		WithMountedCache("/cache/logs", cacheVolume).
		WithDefaultArgs([]string{"sh", "-c", "registry serve /etc/distribution/config.yml | tee /cache/logs/registry.log"}).
		AsService()

	engine := devEngineContainer(c, func(ctr *dagger.Container) *dagger.Container {
		return ctr.
			WithMountedFile("/usr/local/share/ca-certificates/testreg.crt", registryCert)
	},
		engineWithConfig(ctx, t, func(ctx context.Context, t *testctx.T, cfg config.Config) config.Config {
			return config.Config{
				Registries: map[string]config.RegistryConfig{
					"docker.io": {
						Mirrors: []string{"testreg:5000"},
					},
					"testreg:5000": {
						RootCAs: []string{"/usr/local/share/ca-certificates/testreg.crt"},
					},
				},
			}
		})).
		WithServiceBinding("testreg", registry)

	testImagePull(ctx, t, c, engine, cacheVolume)
}

func (EngineSuite) TestRegistryMirrorsHTTP(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	cacheVolume := c.CacheVolume(t.Name())
	registry := c.Container().From("registry:3").
		WithExposedPort(5000, dagger.ContainerWithExposedPortOpts{Protocol: dagger.NetworkProtocolTcp}).
		WithMountedCache("/cache/logs", cacheVolume).
		WithDefaultArgs([]string{"sh", "-c", "registry serve /etc/distribution/config.yml | tee /cache/logs/registry.log"}).
		AsService()

	engine := devEngineContainer(c, engineWithConfig(ctx, t, func(ctx context.Context, t *testctx.T, cfg config.Config) config.Config {
		return config.Config{
			Registries: map[string]config.RegistryConfig{
				"docker.io": {
					Mirrors: []string{"testreg:5000"},
				},
				"testreg:5000": {
					PlainHTTP: ptr(true),
				},
			},
		}
	})).
		WithServiceBinding("testreg", registry)

	testImagePull(ctx, t, c, engine, cacheVolume)
}

func testImagePull(ctx context.Context, t *testctx.T, c *dagger.Client, devEngine *dagger.Container, cacheVolume *dagger.CacheVolume) {
	engineSvc, err := c.Host().Tunnel(devEngineContainerAsService(devEngine)).Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = engineSvc.Stop(ctx) })
	endpoint, err := engineSvc.Endpoint(ctx, dagger.ServiceEndpointOpts{Scheme: "tcp"})
	require.NoError(t, err)
	c2, err := dagger.Connect(ctx, dagger.WithRunnerHost(endpoint), dagger.WithLogOutput(NewTWriter(t)))
	require.NoError(t, err)
	t.Cleanup(func() { c2.Close() })

	out, err := c2.Container().From("alpine:3.22.1@sha256:4bcff63911fcb4448bd4fdacec207030997caf25e9bea4045fa6c8c44de311d1").WithExec([]string{"echo", "hello"}).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello", strings.TrimSpace(out))

	out, err = c.Container().
		From("alpine").
		WithMountedCache("/cache/logs", cacheVolume).
		WithExec([]string{"cat", "/cache/logs/registry.log"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.True(t, strings.Contains(out, "GET /v2/library/alpine"))
}
