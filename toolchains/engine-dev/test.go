package main

import (
	"context"
	"crypto/rand"

	"dagger/engine-dev/internal/dagger"

	"github.com/dagger/dagger/engine/distconsts"
)

// Build and start a dev instance of the dagger engine, suitable
// as a dependency for [engine integration tests](core/integration).
func (dev *EngineDev) TestEngine(
	ctx context.Context,
	ebpfProgs []string, // +optional
	registrySvc *dagger.Service, // +optional
	privateRegistrySvc *dagger.Service, // +optional
	// Cache volume for /run, shared between engine and test container
	// so the test container can access /run/dagger-engine.sock
	engineRunVol *dagger.CacheVolume, // +optional
	// Version to bake into the engine binary via ldflags
	version string, // +optional
	// Tag to bake into the engine binary via ldflags
	tag string, // +optional
) (*dagger.Service, error) {
	// Build the dev engine container with configured eBPF programs and buildkit settings
	devEngine, err := dev.
		WithEBPFProgs(ebpfProgs).
		WithBuildkitConfig(`registry."registry:5000"`, `http = true`).
		WithBuildkitConfig(`registry."privateregistry:5000"`, `http = true`).
		WithBuildkitConfig(`registry."docker.io"`, `mirrors = ["mirror.gcr.io"]`).
		Container(
			ctx,
			"",    // platform
			false, // gpuSupport
			version,
			tag,
		)
	if err != nil {
		return nil, err
	}

	// Mitigation for https://github.com/dagger/dagger/issues/8031 during test suite
	devEngine = devEngine.
		WithEnvVariable("_DAGGER_ENGINE_SYSTEMENV_GODEBUG", "goindex=0")

	if engineRunVol == nil {
		engineRunVol = dag.CacheVolume("dagger-dev-engine-test-varrun" + rand.Text())
	}

	// Use provided registries or create internal ones
	if registrySvc == nil {
		registrySvc = registry()
	}
	if privateRegistrySvc == nil {
		privateRegistrySvc = privateRegistry()
	}

	devEngineSvc := devEngine.
		WithServiceBinding("registry", registrySvc).
		WithServiceBinding("privateregistry", privateRegistrySvc).
		WithExposedPort(1234, dagger.ContainerWithExposedPortOpts{Protocol: dagger.NetworkProtocolTcp}).
		WithMountedCache(distconsts.EngineDefaultStateDir, dag.CacheVolume("dagger-dev-engine-test-state"+rand.Text())).
		WithMountedCache("/run", engineRunVol).
		AsService(dagger.ContainerAsServiceOpts{
			Args: []string{
				"--addr", "unix:///run/dagger-engine.sock",
				"--addr", "tcp://0.0.0.0:1234",
				"--network-name", "dagger-dev",
				"--network-cidr", "10.88.0.0/16",
				"--debugaddr", "0.0.0.0:6060",
			},
			UseEntrypoint:            true,
			InsecureRootCapabilities: true,
		})
	// NOTE: removed manual start, in the hope of making tests orchestration more lazy & efficient.
	// FIXME: double-check this doesn't introduce subtle bugs
	return devEngineSvc, err
}

func registry() *dagger.Service {
	return dag.Container().
		From("registry:2").
		WithExposedPort(5000, dagger.ContainerWithExposedPortOpts{Protocol: dagger.NetworkProtocolTcp}).
		AsService(dagger.ContainerAsServiceOpts{UseEntrypoint: true})
}

func privateRegistry() *dagger.Service {
	const htpasswd = "john:$2y$05$/iP8ud0Fs8o3NLlElyfVVOp6LesJl3oRLYoc3neArZKWX10OhynSC" //nolint:gosec
	return dag.Container().
		From("registry:2").
		WithNewFile("/auth/htpasswd", htpasswd).
		WithEnvVariable("REGISTRY_AUTH", "htpasswd").
		WithEnvVariable("REGISTRY_AUTH_HTPASSWD_REALM", "Registry Realm").
		WithEnvVariable("REGISTRY_AUTH_HTPASSWD_PATH", "/auth/htpasswd").
		WithExposedPort(5000, dagger.ContainerWithExposedPortOpts{Protocol: dagger.NetworkProtocolTcp}).
		AsService(dagger.ContainerAsServiceOpts{UseEntrypoint: true})
}
