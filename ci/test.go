package main

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/remotes/docker"
	"github.com/docker/cli/cli/config/configfile"
	"github.com/moby/buildkit/identity"
	"github.com/moby/buildkit/util/contentutil"
	"github.com/moby/buildkit/version"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/dagger/dagger/ci/internal/dagger"
	"github.com/dagger/dagger/ci/util"
	"github.com/dagger/dagger/engine/distconsts"
)

type Test struct {
	Dagger *Dagger // +private

	CacheConfig string // +private
}

func (t *Test) WithCache(config string) *Test {
	clone := *t
	clone.CacheConfig = config
	return &clone
}

// Run all engine tests
func (t *Test) All(
	ctx context.Context,
	// +optional
	race bool,
) error {
	return t.test(ctx, race, "", "./...")
}

// Run "important" engine tests
func (t *Test) Important(
	ctx context.Context,
	// +optional
	race bool,
) error {
	// These tests give good basic coverage of functionality w/out having to run everything
	return t.test(ctx, race, `^(TestModule|TestContainer)`, "./...")
}

// Run custom engine tests
func (t *Test) Custom(
	ctx context.Context,
	run string,
	// +optional
	// +default="./..."
	pkg string,
	// +optional
	race bool,
) error {
	return t.test(ctx, race, run, pkg)
}

func (t *Test) test(
	ctx context.Context,
	race bool,
	testRegex string,
	pkg string,
) error {
	cgoEnabledEnv := "0"
	args := []string{
		"gotestsum",
		"--format", "testname",
		"--no-color=false",
		"--jsonfile=./tests.log",
		"--",
		// go test flags
		"-parallel=16",
		"-count=1",
		"-timeout=30m",
	}

	if race {
		args = append(args, "-race", "-timeout=1h")
		cgoEnabledEnv = "1"
	}

	if testRegex != "" {
		args = append(args, "-run", testRegex)
	}

	args = append(args, pkg)

	cmd, err := t.testCmd(ctx)
	if err != nil {
		return err
	}

	_, err = cmd.
		WithEnvVariable("CGO_ENABLED", cgoEnabledEnv).
		WithExec(args).
		WithExec([]string{"gotestsum", "tool", "slowest", "--jsonfile=./tests.log", "--threshold=1s"}).
		Sync(ctx)
	return err
}

func toCredentialsFunc(dt string) func(string) (string, string, error) {
	cfg := configfile.New("config.json")
	err := cfg.LoadFromReader(strings.NewReader(dt))
	if err != nil {
		panic(err)
	}

	return func(host string) (string, string, error) {
		if host == "registry-1.docker.io" {
			host = "https://index.docker.io/v1/"
		}
		ac, err := cfg.GetAuthConfig(host)
		if err != nil {
			return "", "", err
		}
		if ac.IdentityToken != "" {
			return "", ac.IdentityToken, nil
		}
		return ac.Username, ac.Password, nil
	}
}

func (t *Test) testCmd(ctx context.Context) (*Container, error) {
	mirror := registryMirror()
	mirror, err := mirror.Start(ctx)
	if err != nil {
		return nil, err
	}
	go mirror.Up(ctx, dagger.ServiceUpOpts{
		Ports: []dagger.PortForward{{
			Backend:  5000,
			Frontend: 5000,
		}},
	})

	var auth docker.Authorizer
	if t.Dagger.HostDockerConfig != nil {
		hostConfig, err := t.Dagger.HostDockerConfig.Plaintext(ctx)
		if err != nil {
			return nil, err
		}
		auth = docker.NewDockerAuthorizer(docker.WithAuthCreds(toCredentialsFunc(hostConfig)), docker.WithAuthClient(http.DefaultClient))
	}

	err = copyImagesLocal(auth, "localhost:5000", map[string]string{
		"library/alpine:latest": "docker.io/library/alpine:3.18.2",
		"library/alpine:3.18":   "docker.io/library/alpine:3.18.2",
		"library/alpine:3.18.2": "docker.io/library/alpine:3.18.2",

		"library/registry:2": "docker.io/library/registry:2",

		"library/golang:latest":        "docker.io/library/golang:1.22.2-alpine",
		"library/golang:1.22":          "docker.io/library/golang:1.22.2-alpine",
		"library/golang:1.22.2":        "docker.io/library/golang:1.22.2-alpine",
		"library/golang:alpine":        "docker.io/library/golang:1.22.2-alpine",
		"library/golang:1.22-alpine":   "docker.io/library/golang:1.22.2-alpine",
		"library/golang:1.22.2-alpine": "docker.io/library/golang:1.22.2-alpine",

		"library/python:latest":    "docker.io/library/python:3.11-slim",
		"library/python:3":         "docker.io/library/python:3.11-slim",
		"library/python:3.11":      "docker.io/library/python:3.11-slim",
		"library/python:3.11-slim": "docker.io/library/python:3.11-slim",
	})
	if err != nil {
		return nil, err
	}

	engine := t.Dagger.Engine().
		WithConfig(`registry."registry:5000"`, `http = true`).
		WithConfig(`registry."privateregistry:5000"`, `http = true`).
		WithConfig(`registry."docker.io"`, `mirrors = ["mirror.dagger.test:5000"]`).
		WithConfig(`registry."mirror.dagger.test:5000"`, `http = true`).
		WithConfig(`grpc`, `address=["unix:///var/run/buildkit/buildkitd.sock", "tcp://0.0.0.0:1234"]`).
		WithArg(`network-name`, `dagger-dev`).
		WithArg(`network-cidr`, `10.88.0.0/16`)
	devEngine, err := engine.Container(ctx, "")
	if err != nil {
		return nil, err
	}

	devBinary, err := t.Dagger.CLI().File(ctx, "")
	if err != nil {
		return nil, err
	}

	// This creates an engine.tar container file that can be used by the integration tests.
	// In particular, it is used by core/integration/remotecache_test.go to create a
	// dev engine that can be used to test remote caching.
	// I also load the dagger binary, so that the remote cache tests can use it to
	// run dagger queries.

	// These are used by core/integration/remotecache_test.go
	testEngineUtils := dag.Directory().
		WithFile("engine.tar", devEngine.AsTarball()).
		WithFile("dagger", devBinary, DirectoryWithFileOpts{
			Permissions: 0755,
		})

	registrySvc := registry()
	devEngineSvc := devEngine.
		WithServiceBinding("registry", registrySvc).
		WithServiceBinding("privateregistry", privateRegistry()).
		WithServiceBinding("mirror.dagger.test", mirror).
		WithExposedPort(1234, ContainerWithExposedPortOpts{Protocol: Tcp}).
		WithMountedCache(distconsts.EngineDefaultStateDir, dag.CacheVolume("dagger-dev-engine-test-state"+identity.NewID())).
		WithExec(nil, ContainerWithExecOpts{
			InsecureRootCapabilities: true,
		}).
		AsService()

	endpoint, err := devEngineSvc.Endpoint(ctx, ServiceEndpointOpts{Port: 1234, Scheme: "tcp"})
	if err != nil {
		return nil, err
	}

	cliBinPath := "/.dagger-cli"

	utilDirPath := "/dagger-dev"
	tests := util.GoBase(t.Dagger.Source).
		WithExec([]string{"go", "install", "gotest.tools/gotestsum@v1.10.0"}).
		WithMountedDirectory("/app", t.Dagger.Source). // need all the source for extension tests
		WithMountedDirectory(utilDirPath, testEngineUtils).
		WithEnvVariable("_DAGGER_TESTS_ENGINE_TAR", filepath.Join(utilDirPath, "engine.tar")).
		WithWorkdir("/app").
		WithServiceBinding("dagger-engine", devEngineSvc).
		WithServiceBinding("registry", registrySvc)

	if t.CacheConfig != "" {
		tests = tests.WithEnvVariable("_EXPERIMENTAL_DAGGER_CACHE_CONFIG", t.CacheConfig)
	}

	// TODO: should use c.Dagger.installer (but this currently can't connect to services)
	tests = tests.
		WithMountedFile(cliBinPath, devBinary).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinPath).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpoint)
	if t.Dagger.HostDockerConfig != nil {
		// this avoids rate limiting in our ci tests
		tests = tests.WithMountedSecret("/root/.docker/config.json", t.Dagger.HostDockerConfig)
	}
	return tests, nil
}

func registry() *Service {
	return dag.Container().
		From("registry:2").
		WithExposedPort(5000, ContainerWithExposedPortOpts{Protocol: Tcp}).
		WithExec(nil).
		AsService()
}

func privateRegistry() *Service {
	const htpasswd = "john:$2y$05$/iP8ud0Fs8o3NLlElyfVVOp6LesJl3oRLYoc3neArZKWX10OhynSC" //nolint:gosec
	return dag.Container().
		From("registry:2").
		WithNewFile("/auth/htpasswd", ContainerWithNewFileOpts{Contents: htpasswd}).
		WithEnvVariable("REGISTRY_AUTH", "htpasswd").
		WithEnvVariable("REGISTRY_AUTH_HTPASSWD_REALM", "Registry Realm").
		WithEnvVariable("REGISTRY_AUTH_HTPASSWD_PATH", "/auth/htpasswd").
		WithExposedPort(5000, ContainerWithExposedPortOpts{Protocol: Tcp}).
		WithExec(nil).
		AsService()
}

func registryMirror() *Service {
	return dag.Container().
		From("registry:2").
		WithEnvVariable("LOG_LEVEL", "warn").
		WithEnvVariable("REGISTRY_STORAGE_FILESYSTEM_ROOTDIRECTORY", "/data/registry").
		WithMountedCache("/data/registry", dag.CacheVolume("dagger-registry-mirror-cache")).
		WithExposedPort(5000, ContainerWithExposedPortOpts{Protocol: Tcp}).
		WithExec(nil).
		AsService()
}

var localImageCache map[string]map[string]struct{}

func copyImagesLocal(auth docker.Authorizer, host string, images map[string]string) error {
	for to, from := range images {
		if localImageCache == nil {
			localImageCache = map[string]map[string]struct{}{}
		}
		if _, ok := localImageCache[host]; !ok {
			localImageCache[host] = map[string]struct{}{}
		}
		if _, ok := localImageCache[host][to]; ok {
			continue
		}
		localImageCache[host][to] = struct{}{}

		start := time.Now()

		var desc ocispecs.Descriptor
		var provider content.Provider
		var err error
		desc, provider, err = ProviderFromRef(from, auth)
		if err != nil {
			return err
		}

		// already exists check
		_, _, err = docker.NewResolver(docker.ResolverOptions{}).Resolve(context.TODO(), host+"/"+to)
		if err == nil {
			fmt.Printf("copied %s to local mirror %s (skipped)\n", from, host+"/"+to)
			continue
		}

		ingester, err := contentutil.IngesterFromRef(host + "/" + to)
		if err != nil {
			return err
		}
		if err := contentutil.CopyChain(context.TODO(), ingester, provider, desc); err != nil {
			return err
		}
		fmt.Printf("copied %s to local mirror %s in %s\n", from, host+"/"+to, time.Since(start))
	}
	return nil
}

func ProviderFromRef(ref string, auth docker.Authorizer) (ocispecs.Descriptor, content.Provider, error) {
	headers := http.Header{}
	headers.Set("User-Agent", version.UserAgent())
	remote := docker.NewResolver(docker.ResolverOptions{
		Headers:    headers,
		Authorizer: auth,
	})

	name, desc, err := remote.Resolve(context.TODO(), ref)
	if err != nil {
		return ocispecs.Descriptor{}, nil, err
	}

	fetcher, err := remote.Fetcher(context.TODO(), name)
	if err != nil {
		return ocispecs.Descriptor{}, nil, err
	}
	return desc, contentutil.FromFetcher(fetcher), nil
}
