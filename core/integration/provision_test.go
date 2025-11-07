package core

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"golang.org/x/mod/semver"
)

type ProvisionSuite struct{}

func TestProvision(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(ProvisionSuite{})
}

var driverTestCases = []struct {
	name      string
	driver    string
	provision func(ctx context.Context, t *testctx.T, dag *dagger.Client, opts containerSetupOpts) *dagger.Container
}{
	{
		name:      "docker",
		driver:    "image+docker",
		provision: dockerSetup,
	},
	{
		name:      "nerdctl",
		driver:    "image+nerdctl",
		provision: nerdctlSetup,
	},
	{
		name:      "podman",
		driver:    "image+podman",
		provision: podmanSetup,
	},
}

func (ProvisionSuite) TestImageDriver(ctx context.Context, t *testctx.T) {
	for _, tc := range driverTestCases {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			t.Run("default image", func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)
				dockerc := tc.provision(ctx, t, c, containerSetupOpts{name: t.Name()})
				dockerc = dockerc.WithMountedFile("/bin/dagger", daggerCliFile(t, c))
				// HACK: pre-download builtin image tag (since the original might not
				// actually have been pushed to the registry)
				dockerc, err := doLoadEngine(ctx, c, dockerc, tc.name, "registry.dagger.io/engine:"+engine.Tag)
				require.NoError(t, err)

				require.True(t, semver.IsValid(detectEngineVersion(ctx, t, dockerc)))
			})

			t.Run("specified image", func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)
				version := "v0.16.1"
				dockerc := tc.provision(ctx, t, c, containerSetupOpts{name: t.Name()}).
					WithMountedFile("/bin/dagger", daggerCliFile(t, c)).
					WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", tc.driver+"://registry.dagger.io/engine:"+version)
				require.Equal(t, version, detectEngineVersion(ctx, t, dockerc))
			})

			t.Run("current image", func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)
				dockerc := tc.provision(ctx, t, c, containerSetupOpts{name: t.Name()})
				dockerc = dockerc.WithMountedFile("/bin/dagger", daggerCliFile(t, c))
				dockerc, err := doLoadEngine(ctx, c, dockerc, tc.name, "registry.dagger.io/engine:dev")
				require.NoError(t, err)
				dockerc = dockerc.
					WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", tc.driver+"://registry.dagger.io/engine:dev")

				require.Equal(t, engine.Version, detectEngineVersion(ctx, t, dockerc))
			})
		})
	}
}

func (ProvisionSuite) TestImageDriverConfig(ctx context.Context, t *testctx.T) {
	for _, tc := range driverTestCases {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			configContents := `{"gc":{"reservedSpace": 1000, "maxUsedSpace": 2000, "minFreeSpace": 3000}}`
			middleware := func(ctr *dagger.Container) *dagger.Container {
				// this mounts the file into both the client+server containers
				return ctr.WithNewFile("/root/.config/dagger/engine.json", configContents)
			}

			dockerc := tc.provision(ctx, t, c, containerSetupOpts{name: t.Name(), middleware: middleware})
			dockerc, err := doLoadEngine(ctx, c, dockerc, tc.name, "registry.dagger.io/engine:dev")
			require.NoError(t, err)
			dockerc = dockerc.
				WithMountedFile("/bin/dagger", daggerCliFile(t, c)).
				WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", tc.driver+"://registry.dagger.io/engine:dev")

			// check that the config was used by the engine
			out, err := dockerc.WithExec([]string{"dagger", "query", "-M"}, dagger.ContainerWithExecOpts{Stdin: "{engine{localCache{reservedSpace,maxUsedSpace,minFreeSpace}}}", InsecureRootCapabilities: true}).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"engine": {"localCache": {"reservedSpace": 1000, "maxUsedSpace": 2000, "minFreeSpace": 3000}}}`, out)

			// also, just for good measure, check that the file was propagated to the right place
			ctrid, err := dockerc.WithExec([]string{tc.name, "ps", "-n1", "--format={{.ID}}"}).Stdout(ctx)
			require.NoError(t, err)
			ctrid = strings.TrimSpace(ctrid)
			require.NotEmpty(t, ctrid)
			result, err := dockerc.WithExec([]string{tc.name, "exec", ctrid, "cat", "/etc/dagger/engine.json"}).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, configContents, result)
		})
	}
}

func (ProvisionSuite) TestImageDriverCACerts(ctx context.Context, t *testctx.T) {
	// tests that custom CA certs are properly propagated to the engine container when provisioned
	for _, tc := range driverTestCases {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			fakeCACert := `-----BEGIN CERTIFICATE-----
FAKE CERTIFICATE DATA
-----END CERTIFICATE-----`

			middleware := func(ctr *dagger.Container) *dagger.Container {
				// this mounts the file into both the client+server containers
				return ctr.WithNewFile("/root/.config/dagger/ca-certificates/fake-ca.crt", fakeCACert)
			}

			dockerc := tc.provision(ctx, t, c, containerSetupOpts{name: t.Name(), middleware: middleware})
			dockerc, err := doLoadEngine(ctx, c, dockerc, tc.name, "registry.dagger.io/engine:dev")
			require.NoError(t, err)
			dockerc = dockerc.
				WithMountedFile("/bin/dagger", daggerCliFile(t, c)).
				WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", tc.driver+"://registry.dagger.io/engine:dev")

			// check that the ca-cert was used by the engine
			out, err := dockerc.WithExec([]string{"dagger", "query", "-M"}, dagger.ContainerWithExecOpts{Stdin: `
				query {
					container {
						from(address: "alpine:latest") {
							standalone: withExec(args: ["cat", "/usr/local/share/ca-certificates/fake-ca.crt"]) {
								stdout
							}
							bundle: withExec(args: ["cat", "/etc/ssl/certs/ca-certificates.crt"]) {
								stdout
							}
						}
					}
				}
			`, InsecureRootCapabilities: true}).Stdout(ctx)
			require.NoError(t, err)
			require.Contains(t, gjson.Get(out, "container.from.standalone.stdout").String(), fakeCACert)
			require.Contains(t, gjson.Get(out, "container.from.bundle.stdout").String(), fakeCACert)
		})
	}
}

func (ProvisionSuite) TestImageDriverGarbageCollectEngines(ctx context.Context, t *testctx.T) {
	dockerPs := func(ctx context.Context, t *testctx.T, dockerc *dagger.Container, cli string) []string {
		out, err := dockerc.
			WithEnvVariable("CACHEBUSTER", identity.NewID()).
			WithExec([]string{cli, "ps", "-q"}).
			Stdout(ctx)
		require.NoError(t, err)
		out = strings.TrimSpace(out)
		if out == "" {
			return []string{}
		}
		return strings.Split(out, "\n")
	}

	for _, tc := range driverTestCases {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			t.Run("cleanup", func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)
				dockerc := tc.provision(ctx, t, c, containerSetupOpts{name: t.Name()})
				dockerc = dockerc.WithMountedFile("/bin/dagger", daggerCliFile(t, c))

				require.Len(t, dockerPs(ctx, t, dockerc, tc.name), 0)

				version := "v0.16.1"
				first := dockerc.WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", tc.driver+"://registry.dagger.io/engine:"+version)
				require.Equal(t, version, detectEngineVersion(ctx, t, first))

				require.Len(t, dockerPs(ctx, t, dockerc, tc.name), 1)

				version = "v0.16.0"
				second := dockerc.WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", tc.driver+"://registry.dagger.io/engine:"+version)
				require.Equal(t, version, detectEngineVersion(ctx, t, second))

				require.Len(t, dockerPs(ctx, t, dockerc, tc.name), 1)
			})

			t.Run("no cleanup", func(ctx context.Context, t *testctx.T) {
				if tc.name == "podman" {
					// this is weiiird, nested podman uses host networking everywhere
					t.Skip("nested podman doesn't support multiple running containers")
				}

				c := connect(ctx, t)
				dockerc := tc.provision(ctx, t, c, containerSetupOpts{name: t.Name()})
				dockerc = dockerc.WithMountedFile("/bin/dagger", daggerCliFile(t, c))
				dockerc = dockerc.WithEnvVariable("DAGGER_LEAVE_OLD_ENGINE", "true")

				require.Len(t, dockerPs(ctx, t, dockerc, tc.name), 0)

				version := "v0.16.1"
				first := dockerc.WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", tc.driver+"://registry.dagger.io/engine:"+version)
				require.Equal(t, version, detectEngineVersion(ctx, t, first))

				require.Len(t, dockerPs(ctx, t, dockerc, tc.name), 1)

				version = "v0.16.0"
				second := dockerc.WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", tc.driver+"://registry.dagger.io/engine:"+version)
				require.Equal(t, version, detectEngineVersion(ctx, t, second))

				require.Len(t, dockerPs(ctx, t, dockerc, tc.name), 2)
			})
		})
	}
}

func detectEngineVersion(ctx context.Context, t *testctx.T, ctr *dagger.Container) string {
	out, err := ctr.
		// NOTE: we don't use any interesting functionality, so disable this check
		WithEnvVariable("_EXPERIMENTAL_DAGGER_MIN_VERSION", "v0.0.0").
		WithExec([]string{"dagger", "query", "-M"}, dagger.ContainerWithExecOpts{Stdin: "{version}", InsecureRootCapabilities: true}).
		Stdout(ctx)
	require.NoError(t, err)

	var data struct {
		Version string
	}
	err = json.Unmarshal([]byte(out), &data)
	require.NoError(t, err)

	return data.Version
}

type containerSetupOpts struct {
	name    string
	version string

	middleware func(*dagger.Container) *dagger.Container
}

func dockerSetup(ctx context.Context, t *testctx.T, dag *dagger.Client, opts containerSetupOpts) *dagger.Container {
	middleware := opts.middleware
	if middleware == nil {
		middleware = func(ctr *dagger.Container) *dagger.Container {
			return ctr
		}
	}

	dockerdTag := "dind"
	dockercTag := "cli"
	if opts.version != "" {
		dockerdTag = opts.version + "-" + dockerdTag
		dockercTag = opts.version + "-" + dockercTag
	}

	port := 4000
	dockerd := dag.Container().From("docker:"+dockerdTag).
		With(middleware).
		WithMountedCache("/var/lib/docker", dag.CacheVolume(opts.name+"-"+opts.version+"-docker-lib"), dagger.ContainerWithMountedCacheOpts{
			Sharing: dagger.CacheSharingModePrivate,
		}).
		WithExposedPort(port).
		AsService(
			dagger.ContainerAsServiceOpts{
				Args: []string{
					"dockerd",
					"--host=tcp://0.0.0.0:" + strconv.Itoa(port),
					"--tls=false",
				},
				InsecureRootCapabilities: true,
			},
		)
	dockerd, err := dockerd.Start(ctx)
	require.NoError(t, err)
	dockerHost, err := dockerd.Endpoint(ctx, dagger.ServiceEndpointOpts{Scheme: "tcp"})
	require.NoError(t, err)

	dockerc := dag.Container().From("docker:"+dockercTag).
		With(middleware).
		With(mountDockerConfig(dag)).
		WithServiceBinding("docker", dockerd).
		WithEnvVariable("DOCKER_HOST", dockerHost).
		WithEnvVariable("CACHEBUSTER", identity.NewID()).
		WithWorkdir("/work")

	t.Cleanup(func() {
		_, err := dockerc.WithExec([]string{"sh", "-c", "docker rm -f $(docker ps -aq); docker system prune --force --all --volumes; true"}).Sync(ctx)
		require.NoError(t, err)

		_, err = dockerd.Stop(ctx)
		require.NoError(t, err)
	})

	return dockerc
}

func podmanSetup(ctx context.Context, t *testctx.T, dag *dagger.Client, opts containerSetupOpts) *dagger.Container {
	middleware := opts.middleware
	if middleware == nil {
		middleware = func(ctr *dagger.Container) *dagger.Container {
			return ctr
		}
	}

	port := 4000
	base := dag.Container().
		From("quay.io/podman/stable:" + cmp.Or(opts.version, "latest")).
		With(middleware)
	podman := base.
		WithMountedCache("/var/lib/containers", dag.CacheVolume(opts.name+"-"+opts.version+"-podman-lib"), dagger.ContainerWithMountedCacheOpts{
			Sharing: dagger.CacheSharingModePrivate,
		}).
		WithExposedPort(port).
		AsService(
			dagger.ContainerAsServiceOpts{
				Args:                     []string{"podman", "system", "service", fmt.Sprintf("tcp://0.0.0.0:%d", port), "--time=0"},
				InsecureRootCapabilities: true,
			},
		)
	podman, err := podman.Start(ctx)
	require.NoError(t, err)
	podmanHost, err := podman.Endpoint(ctx, dagger.ServiceEndpointOpts{Scheme: "tcp"})
	require.NoError(t, err)

	dockerc := base.
		With(mountDockerConfig(dag)).
		WithServiceBinding("podman", podman).
		WithEnvVariable("CONTAINER_HOST", podmanHost).
		WithEnvVariable("CACHEBUSTER", identity.NewID()).
		WithWorkdir("/work")

	t.Cleanup(func() {
		_, err := dockerc.WithExec([]string{"sh", "-c", "podman rm -f $(podman ps -aq); podman system prune --force --all --volumes; true"}).Sync(ctx)
		require.NoError(t, err)

		_, err = podman.Stop(ctx)
		require.NoError(t, err)
	})

	return dockerc
}

func nerdctlSetup(ctx context.Context, t *testctx.T, dag *dagger.Client, opts containerSetupOpts) *dagger.Container {
	repo := dag.Git("https://github.com/containerd/nerdctl.git")
	var ref *dagger.GitRef
	if opts.version == "" {
		ref = repo.Tag("v2.1.2")
	} else {
		ref = repo.Tag(opts.version)
	}

	// build nerdctl from scratch (annoying, but there *is no upstream package*)
	base := ref.Tree().
		DockerBuild().
		WithMountedCache("/run/containerd", dag.CacheVolume(opts.name+"-run-containerd")).
		WithMountedCache("/var/lib/containerd", dag.CacheVolume(opts.name+"-containerd")).
		WithMountedCache("/var/lib/buildkit", dag.CacheVolume(opts.name+"-buildkit")).
		WithMountedCache("/var/lib/containerd-stargz-grpc", dag.CacheVolume(opts.name+"-containerd-stargz-grpc")).
		WithMountedCache("/var/lib/nerdctl", dag.CacheVolume(opts.name+"-nerdctl")).
		WithEnvVariable("CACHEBUST", rand.Text()) // use a new service every test run
	if opts.middleware != nil {
		base = base.With(opts.middleware)
	}

	svc := base.AsService(dagger.ContainerAsServiceOpts{
		Args:                     []string{"containerd"},
		InsecureRootCapabilities: true,
	})
	svc, err := svc.Start(ctx)
	require.NoError(t, err)

	ctr := base.WithServiceBinding("containerd", svc)

	// try stat'ing /run/containerd/containerd.sock every 1 second, up to 30s. That's what we connect to so
	// can't rely on network health checks atm
	_, err = ctr.
		WithEnvVariable("CACHEBUSTER", rand.Text()).
		WithExec([]string{"sh", "-c", `
			for i in $(seq 1 30); do
				if stat /run/containerd/containerd.sock; then
					exit 0
				else
					sleep 1
				fi
			done
			exit 1
			`,
		}).
		Sync(ctx)
	require.NoError(t, err)

	t.Cleanup(func() {
		opts := dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeAny, InsecureRootCapabilities: true}
		_, err := ctr.
			WithEnvVariable("CACHEBUSTER", identity.NewID()).
			WithExec([]string{"sh", "-c", "nerdctl rm -f $(nerdctl ps -aq)"}, opts).
			WithExec([]string{"sh", "-c", "ctr image rm $(ctr image ls -q)"}, opts).
			WithExec([]string{"sh", "-c", "ctr content rm $(ctr content ls -q)"}, opts).
			Sync(ctx)
		require.NoError(t, err)

		_, err = svc.Stop(ctx)
		require.NoError(t, err)
	})

	return ctr
}

func dockerLoadEngine(ctx context.Context, dag *dagger.Client, ctr *dagger.Container, engineTag string) (*dagger.Container, error) {
	return doLoadEngine(ctx, dag, ctr, "docker", engineTag)
}
func nerdctlLoadEngine(ctx context.Context, dag *dagger.Client, ctr *dagger.Container, engineTag string) (*dagger.Container, error) {
	return doLoadEngine(ctx, dag, ctr, "nerdctl", engineTag)
}
func doLoadEngine(ctx context.Context, dag *dagger.Client, ctr *dagger.Container, cli string, engineTag string) (*dagger.Container, error) {
	var tarPath string
	if v, ok := os.LookupEnv("_DAGGER_TESTS_ENGINE_TAR"); ok {
		tarPath = v
	} else {
		tarPath = "./bin/engine.tar"
	}
	out, err := ctr.
		WithMountedFile("engine.tar", dag.Host().File(tarPath)).
		WithEnvVariable("CACHEBUSTER", rand.Text()).
		WithExec([]string{cli, "image", "load", "-i", "engine.tar"}).
		Stdout(ctx)
	if err != nil {
		return nil, err
	}
	result := regexp.MustCompile("sha256:([0-9a-f]+)").FindStringSubmatch(out)
	if len(result) == 0 {
		return nil, fmt.Errorf("unexpected output from docker load: %s", out)
	}
	imageID := result[1]
	_, err = ctr.WithExec([]string{cli, "tag", imageID, engineTag}).Sync(ctx)
	if err != nil {
		return nil, err
	}

	return ctr, nil
}

// mountDockerConfig is a helper for mounting the host's docker config if it exists
func mountDockerConfig(dag *dagger.Client) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		home, err := os.UserHomeDir()
		if err != nil {
			return ctr
		}
		content, err := os.ReadFile(filepath.Join(home, ".docker/config.json"))
		if err != nil {
			return ctr
		}

		return ctr.WithMountedSecret(
			"/root/.docker/config.json",
			dag.SetSecret("docker-config-"+identity.NewID(), string(content)),
		)
	}
}
