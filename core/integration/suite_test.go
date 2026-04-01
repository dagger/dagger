package core

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/md5"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/dagger/internal/testutil/dagger"
	"github.com/dagger/dagger/internal/testutil/dagger/dag"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/core"

	"github.com/dagger/testctx"
	"github.com/dagger/testctx/oteltest"
)

var (
	baseOnce      sync.Once
	registryOnce  sync.Once
	engineTarOnce sync.Once
	engineOnce    sync.Once

	engineStarted          bool
	startedRegistry        *dagger.Service
	startedPrivateRegistry *dagger.Service
)

// ensureBase computes version/tag and exports the CLI binary.
// Uses dag.* (outer session).
func ensureBase(ctx context.Context) {
	baseOnce.Do(func() {
		version, err := dag.Version().Version(ctx)
		if err != nil {
			panic(fmt.Sprintf("ensureBase: failed to get version: %v", err))
		}
		tag, err := dag.Version().ImageTag(ctx)
		if err != nil {
			panic(fmt.Sprintf("ensureBase: failed to get image tag: %v", err))
		}
		os.Setenv("_EXPERIMENTAL_DAGGER_VERSION", version)
		os.Setenv("_EXPERIMENTAL_DAGGER_TAG", tag)
		engine.Version = version
		engine.Tag = tag

		_, err = dag.Cli().Binary().Export(ctx, "/.dagger-cli")
		if err != nil {
			panic(fmt.Sprintf("ensureBase: failed to export CLI binary: %v", err))
		}
		os.Setenv("_EXPERIMENTAL_DAGGER_CLI_BIN", "/.dagger-cli")
		os.Setenv("_TEST_DAGGER_CLI_LINUX_BIN", "/.dagger-cli")

		os.Setenv("_DAGGER_TESTS_REPO_PATH", "../..")
	})
}

// ensureRegistries creates and starts registry services.
// Must be called BEFORE ensureEngine.
func ensureRegistries(ctx context.Context) {
	registryOnce.Do(func() {
		if engineStarted {
			panic("ensureRegistries must be called before ensureEngine")
		}
		ensureBase(ctx)

		registrySvc := dag.Container().
			From("registry:2").
			WithExposedPort(5000, dagger.ContainerWithExposedPortOpts{Protocol: dagger.NetworkProtocolTcp}).
			AsService(dagger.ContainerAsServiceOpts{UseEntrypoint: true})

		const htpasswd = "john:$2y$05$/iP8ud0Fs8o3NLlElyfVVOp6LesJl3oRLYoc3neArZKWX10OhynSC"
		privateRegistrySvc := dag.Container().
			From("registry:2").
			WithNewFile("/auth/htpasswd", htpasswd).
			WithEnvVariable("REGISTRY_AUTH", "htpasswd").
			WithEnvVariable("REGISTRY_AUTH_HTPASSWD_REALM", "Registry Realm").
			WithEnvVariable("REGISTRY_AUTH_HTPASSWD_PATH", "/auth/htpasswd").
			WithExposedPort(5000, dagger.ContainerWithExposedPortOpts{Protocol: dagger.NetworkProtocolTcp}).
			AsService(dagger.ContainerAsServiceOpts{UseEntrypoint: true})

		var err error
		startedRegistry, err = registrySvc.Start(ctx)
		if err != nil {
			panic(fmt.Sprintf("ensureRegistries: failed to start registry: %v", err))
		}
		startedPrivateRegistry, err = privateRegistrySvc.Start(ctx)
		if err != nil {
			panic(fmt.Sprintf("ensureRegistries: failed to start private registry: %v", err))
		}

		registryEndpoint, err := startedRegistry.Endpoint(ctx, dagger.ServiceEndpointOpts{Port: 5000, Scheme: "tcp"})
		if err != nil {
			panic(fmt.Sprintf("ensureRegistries: failed to get registry endpoint: %v", err))
		}
		privateRegistryEndpoint, err := startedPrivateRegistry.Endpoint(ctx, dagger.ServiceEndpointOpts{Port: 5000, Scheme: "tcp"})
		if err != nil {
			panic(fmt.Sprintf("ensureRegistries: failed to get private registry endpoint: %v", err))
		}
		registryHost = mustParseEndpointHost(registryEndpoint)
		privateRegistryHost = mustParseEndpointHost(privateRegistryEndpoint)
	})
}

// ensureEngineTar builds and exports engine.tar for tests that spin up
// additional dev engines. Must be called BEFORE ensureEngine.
func ensureEngineTar(ctx context.Context) {
	engineTarOnce.Do(func() {
		ensureBase(ctx)

		engineDev := dag.EngineDev()
		if registryHost != "" {
			engineDev = engineDev.
				WithBuildkitConfig(fmt.Sprintf(`registry."%s"`, registryHost), `http = true`).
				WithBuildkitConfig(fmt.Sprintf(`registry."%s"`, privateRegistryHost), `http = true`)
		}
		engineTar := engineDev.
			WithBuildkitConfig(`registry."registry:5000"`, `http = true`).
			WithBuildkitConfig(`registry."privateregistry:5000"`, `http = true`).
			WithBuildkitConfig(`registry."docker.io"`, `mirrors = ["mirror.gcr.io"]`).
			Container(dagger.EngineDevContainerOpts{Version: engine.Version, Tag: engine.Tag}).
			AsTarball()
		if _, err := engineTar.Export(ctx, "/tmp/engine.tar"); err != nil {
			panic(fmt.Sprintf("ensureEngineTar: failed to export engine tar: %v", err))
		}
		os.Setenv("_DAGGER_TESTS_ENGINE_TAR", "/tmp/engine.tar")
	})
}

// ensureEngine starts the inner engine and unsets session vars.
// After this, dag.* calls no longer work. All ensure* helpers that
// need dag.* must be called before this.
func ensureEngine(ctx context.Context) {
	engineOnce.Do(func() {
		ensureEngineTar(ctx)
		ensureBase(ctx)

		engineDev := dag.EngineDev()
		if registryHost != "" {
			engineDev = engineDev.
				WithBuildkitConfig(fmt.Sprintf(`registry."%s"`, registryHost), `http = true`).
				WithBuildkitConfig(fmt.Sprintf(`registry."%s"`, privateRegistryHost), `http = true`)
		}
		engineSvc, err := engineDev.TestEngine(dagger.EngineDevTestEngineOpts{
			RegistrySvc:        startedRegistry,
			PrivateRegistrySvc: startedPrivateRegistry,
			Version:            engine.Version,
			Tag:                engine.Tag,
		}).Start(ctx)
		if err != nil {
			if info, ok := asExecError(err); ok {
				panic(fmt.Sprintf("ensureEngine: engine failed to start: %v\nStdout: %s\nStderr: %s", err, info.Stdout, info.Stderr))
			}
			panic(fmt.Sprintf("ensureEngine: engine failed to start: %v", err))
		}

		endpoint, err := engineSvc.Endpoint(ctx, dagger.ServiceEndpointOpts{Port: 1234, Scheme: "tcp"})
		if err != nil {
			panic(fmt.Sprintf("ensureEngine: failed to get engine endpoint: %v", err))
		}
		os.Setenv("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpoint)

		// Point of no return: unset session vars so connect() routes to inner engine
		os.Unsetenv("DAGGER_SESSION_PORT")
		os.Unsetenv("DAGGER_SESSION_TOKEN")
		engineStarted = true
	})
}

func TestMain(m *testing.M) {
	origAuthSock := os.Getenv("SSH_AUTH_SOCK")
	os.Unsetenv("SSH_AUTH_SOCK")

	res := oteltest.Main(m)
	if origAuthSock != "" {
		os.Setenv("SSH_AUTH_SOCK", origAuthSock)
	}
	os.Exit(res)
}

func Middleware() []testctx.Middleware[*testing.T] {
	return []testctx.Middleware[*testing.T]{
		testctx.WithParallel(),
		oteltest.WithTracing(
			oteltest.TraceConfig[*testing.T]{
				StartOptions: testutil.SpanOpts[*testing.T],
			},
		),
		oteltest.WithLogging[*testing.T](),
	}
}

func BenchMiddleware() []testctx.Middleware[*testing.B] {
	return []testctx.Middleware[*testing.B]{
		oteltest.WithTracing(
			oteltest.TraceConfig[*testing.B]{
				StartOptions: testutil.SpanOpts[*testing.B],
			},
		),
		oteltest.WithLogging[*testing.B](),
	}
}

func connect(ctx context.Context, t testing.TB, opts ...dagger.ClientOpt) *dagger.Client {
	opts = append([]dagger.ClientOpt{
		// FIXME: test spans are easier to read in the TUI when this is silenced
		dagger.WithLogOutput(io.Discard),
	}, opts...)
	client, err := dagger.Connect(ctx, opts...)
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })
	return client
}

func newCache(t *testctx.T) core.CacheVolumeID {
	res, err := Query[struct {
		CacheVolume struct {
			ID core.CacheVolumeID
		}
	}](t, `
		query CreateCache($key: String!) {
			cacheVolume(key: $key) {
				id
			}
		}
	`, &QueryOptions{Variables: map[string]any{
		"key": identity.NewID(),
	}})
	require.NoError(t, err)

	return res.CacheVolume.ID
}

func newDirWithFile(t *testctx.T, path, contents string) core.DirectoryID {
	res, err := Query[struct {
		Directory struct {
			WithNewFile struct {
				ID core.DirectoryID
			}
		}
	}](t,
		`query Test($path: String!, $contents: String!) {
			directory {
				withNewFile(path: $path, contents: $contents) {
					id
				}
			}
		}`, &QueryOptions{Variables: map[string]any{
			"path":     path,
			"contents": contents,
		}})
	require.NoError(t, err)

	return res.Directory.WithNewFile.ID
}

func newFile(t *testctx.T, path, contents string) core.FileID {
	res, err := Query[struct {
		Directory struct {
			WithNewFile struct {
				File struct {
					ID core.FileID
				}
			}
		}
	}](t,
		`query Test($path: String!, $contents: String!) {
			directory {
				withNewFile(path: $path, contents: $contents) {
					file(path: "some-file") {
						id
					}
				}
			}
		}`, &QueryOptions{Variables: map[string]any{
			"path":     path,
			"contents": contents,
		}})
	require.NoError(t, err)

	fileID := res.Directory.WithNewFile.File.ID
	require.NotEmpty(t, fileID)

	return fileID
}

var (
	// Set by TestMain to the registry endpoints reachable from the test process.
	registryHost        string
	privateRegistryHost string
)

func registryRef(name string) string {
	if registryHost == "" {
		panic("registryHost not configured — call ensureRegistries before ensureEngine")
	}
	return fmt.Sprintf("%s/%s:%s", registryHost, name, identity.NewID())
}

func privateRegistryRef(name string) string {
	if privateRegistryHost == "" {
		panic("privateRegistryHost not configured — call ensureRegistries before ensureEngine")
	}
	return fmt.Sprintf("%s/%s:%s", privateRegistryHost, name, identity.NewID())
}

func ls(dir string) ([]string, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	names := make([]string, len(ents))
	for i, ent := range ents {
		names[i] = ent.Name()
	}
	return names, nil
}

func tarEntries(t testing.TB, path string) []string {
	f, err := os.Open(path)
	require.NoError(t, err)

	entries := []string{}
	tr := tar.NewReader(f)
	for {
		hdr, err := tr.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			require.NoError(t, err)
		}

		entries = append(entries, hdr.Name)
	}

	return entries
}

func readTarFile(t testing.TB, pathToTar, pathInTar string) []byte {
	f, err := os.Open(pathToTar)
	require.NoError(t, err)

	tr := tar.NewReader(f)
	for {
		hdr, err := tr.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			require.NoError(t, err)
		}

		if hdr.Name == pathInTar {
			b, err := io.ReadAll(tr)
			require.NoError(t, err)
			return b
		}
	}

	return nil
}

func computeMD5FromReader(reader io.Reader) string {
	h := md5.New()
	io.Copy(h, reader)
	return fmt.Sprintf("%x", h.Sum(nil))
}

func daggerCliPath(t testing.TB) string {
	t.Helper()
	cliPath := os.Getenv("_EXPERIMENTAL_DAGGER_CLI_BIN")
	if cliPath == "" {
		var err error
		cliPath, err = exec.LookPath("dagger")
		require.NoError(t, err)
	}
	if cliPath == "" {
		t.Log("missing _EXPERIMENTAL_DAGGER_CLI_BIN")
		t.FailNow()
	}
	return cliPath
}

func daggerLinuxCliPath(t testing.TB) string {
	if runtime.GOOS == "linux" {
		return daggerCliPath(t)
	}
	cliPath := os.Getenv("_TEST_DAGGER_CLI_LINUX_BIN")
	if cliPath == "" {
		t.Log("missing _TEST_DAGGER_CLI_LINUX_BIN")
		t.FailNow()
	}
	return cliPath
}

func daggerCliFile(t testing.TB, c *dagger.Client) *dagger.File {
	// This loads the dagger-cli binary from the host into the container, that
	// was set up by the test caller. This is used to communicate with the dev
	// engine.
	t.Helper()
	return c.Host().File(daggerLinuxCliPath(t))
}

func daggerCliBase(t testing.TB, c *dagger.Client) *dagger.Container {
	t.Helper()
	return c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work")
}

const testCLIBinPath = "/bin/dagger"

func goCache(c *dagger.Client) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.
			WithMountedCache("/go/pkg/mod", c.CacheVolume("go-mod")).
			WithEnvVariable("GOMODCACHE", "/go/pkg/mod").
			WithMountedCache("/go/build-cache", c.CacheVolume("go-build")).
			WithEnvVariable("GOCACHE", "/go/build-cache")
	}
}

type safeBuffer struct {
	bu bytes.Buffer
	mu sync.Mutex
}

func (s *safeBuffer) Write(p []byte) (n int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bu.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bu.String()
}

func mustParseEndpointHost(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		panic(fmt.Sprintf("failed to parse endpoint %q: %v", endpoint, err))
	}
	return u.Host
}

func limitTicker(interval time.Duration, limit int) <-chan time.Time {
	ch := make(chan time.Time, limit)
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		defer close(ch)
		for range limit {
			ch <- <-ticker.C
		}
	}()
	return ch
}

// ensure the cache mount doesn't get pruned in the middle of the test by having a container
// run throughout with the cache mounted as a service dependency
func preventCacheMountPrune(c *dagger.Client, t *testctx.T, cache *dagger.CacheVolume, opts ...dagger.ContainerWithMountedCacheOpts) dagger.WithContainerFunc {
	t.Helper()
	svc, err := c.Container().
		From(alpineImage).
		WithExec([]string{"apk", "add", "socat"}).
		WithMountedCache("/cache", cache, opts...).
		WithDefaultArgs([]string{"socat", "-v", "tcp-l:2345,fork", "exec:/bin/cat"}).
		WithExposedPort(2345).
		AsService().
		Start(t.Context())
	require.NoError(t, err)

	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.WithServiceBinding("cachemountsaver", svc)
	}
}

// execErrorInfo holds ExecError fields extracted from any error that carries
// EXEC_ERROR extensions, regardless of which Go package defined the ExecError type.
// This is needed because the internal testutil SDK and the published SDK define
// separate ExecError types that are not interchangeable via errors.As.
type execErrorInfo struct {
	Cmd      []string
	ExitCode int
	Stdout   string
	Stderr   string
}

// asExecError extracts ExecError information from an error chain. It uses the
// Extensions() interface to handle ExecError types from any package.
func asExecError(err error) (*execErrorInfo, bool) {
	// Try the typed approach first (works when types match)
	var execErr *dagger.ExecError
	if errors.As(err, &execErr) {
		return &execErrorInfo{
			Cmd:      execErr.Cmd,
			ExitCode: execErr.ExitCode,
			Stdout:   execErr.Stdout,
			Stderr:   execErr.Stderr,
		}, true
	}

	// Fall back to interface-based extraction for cross-package ExecError types
	type hasExtensions interface {
		Extensions() map[string]any
	}
	var ext hasExtensions
	if !errors.As(err, &ext) {
		return nil, false
	}
	extensions := ext.Extensions()
	if extensions["_type"] != "EXEC_ERROR" {
		return nil, false
	}
	info := &execErrorInfo{}
	if code, ok := extensions["exitCode"].(float64); ok {
		info.ExitCode = int(code)
	}
	if args, ok := extensions["cmd"].([]interface{}); ok {
		for _, a := range args {
			if s, ok := a.(string); ok {
				info.Cmd = append(info.Cmd, s)
			}
		}
	}
	if stdout, ok := extensions["stdout"].(string); ok {
		info.Stdout = stdout
	}
	if stderr, ok := extensions["stderr"].(string); ok {
		info.Stderr = stderr
	}
	return info, true
}

// requireErrOut is the same as require.ErrorContains, except it also looks in
// the Stdout/Stderr of a *dagger.ExecErr, since that's something we do a lot
// in tests.
//
// TODO: A better alternative might be to record the log output and assert
// against what the user sees there, but that's a bigger lift.
func requireErrOut(t *testctx.T, err error, out string, msgAndInterface ...any) {
	t.Helper()
	if err == nil {
		require.Fail(t, "expected error, got nil")
	}
	if info, ok := asExecError(err); ok {
		require.Contains(
			t,
			fmt.Sprintf("%s\nStdout: %s\nStderr: %s", err, info.Stdout, info.Stderr),
			out,
			msgAndInterface...,
		)
		return
	}
	require.ErrorContains(t, err, out)
}

// requireErrRegexp is the same as require.Regexp against err.Error(), except
// it also looks in the Stdout/Stderr of a *dagger.ExecErr, since that's
// something we do a lot in tests.
//
// TODO: A better alternative might be to record the log output and assert
// against what the user sees there, but that's a bigger lift.
func requireErrRegexp(t *testctx.T, err error, re string) {
	t.Helper()
	if err == nil {
		require.Fail(t, "expected error, got nil")
	}
	if info, ok := asExecError(err); ok {
		require.Regexp(
			t,
			re,
			fmt.Sprintf("%s\nStdout: %s\nStderr: %s", err, info.Stdout, info.Stderr),
		)
		return
	}
	require.Regexp(t, re, err.Error())
}
