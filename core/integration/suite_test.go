package core

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/md5"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/dagger/testctx/oteltest"
)

func TestMain(m *testing.M) {
	// Preserve original SSH_AUTH_SOCK value and
	// Ensure SSH_AUTH_SOCK does not pollute tests state
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
		dagger.WithLogOutput(testutil.NewTWriter(t)),
	}, opts...)
	client, err := dagger.Connect(ctx, opts...)
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })
	return client
}

func newCache(t *testctx.T) core.CacheVolumeID {
	var res struct {
		CacheVolume struct {
			ID core.CacheVolumeID
		}
	}

	err := testutil.Query(t, `
		query CreateCache($key: String!) {
			cacheVolume(key: $key) {
				id
			}
		}
	`, &res, &testutil.QueryOptions{Variables: map[string]any{
		"key": identity.NewID(),
	}})
	require.NoError(t, err)

	return res.CacheVolume.ID
}

func newDirWithFile(t *testctx.T, path, contents string) core.DirectoryID {
	dirRes := struct {
		Directory struct {
			WithNewFile struct {
				ID core.DirectoryID
			}
		}
	}{}

	err := testutil.Query(t,
		`query Test($path: String!, $contents: String!) {
			directory {
				withNewFile(path: $path, contents: $contents) {
					id
				}
			}
		}`, &dirRes, &testutil.QueryOptions{Variables: map[string]any{
			"path":     path,
			"contents": contents,
		}})
	require.NoError(t, err)

	return dirRes.Directory.WithNewFile.ID
}

func newFile(t *testctx.T, path, contents string) core.FileID {
	var secretRes struct {
		Directory struct {
			WithNewFile struct {
				File struct {
					ID core.FileID
				}
			}
		}
	}

	err := testutil.Query(t,
		`query Test($path: String!, $contents: String!) {
			directory {
				withNewFile(path: $path, contents: $contents) {
					file(path: "some-file") {
						id
					}
				}
			}
		}`, &secretRes, &testutil.QueryOptions{Variables: map[string]any{
			"path":     path,
			"contents": contents,
		}})
	require.NoError(t, err)

	fileID := secretRes.Directory.WithNewFile.File.ID
	require.NotEmpty(t, fileID)

	return fileID
}

const (
	registryHost        = "registry:5000"
	privateRegistryHost = "privateregistry:5000"
)

func registryRef(name string) string {
	return fmt.Sprintf("%s/%s:%s", registryHost, name, identity.NewID())
}

func privateRegistryRef(name string) string {
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

func limitTicker(interval time.Duration, limit int) <-chan time.Time {
	ch := make(chan time.Time, limit)
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		defer close(ch)
		for i := 0; i < limit; i++ {
			ch <- <-ticker.C
		}
	}()
	return ch
}

// ensure the cache mount doesn't get pruned in the middle of the test by having a container
// run throughout with the cache mounted
func preventCacheMountPrune(ctx context.Context, t *testctx.T, c *dagger.Client, cache *dagger.CacheVolume, opts ...dagger.ContainerWithMountedCacheOpts) func() error {
	t.Helper()
	ctx, cancel := context.WithCancelCause(ctx)
	cancelErr := errors.New("test done")
	t.Cleanup(func() {
		cancel(cancelErr)
	})
	defer cancel(cancelErr)
	var eg errgroup.Group
	eg.Go(func() error {
		_, err := c.Container().
			From(alpineImage).
			WithMountedCache("/cache", cache, opts...).
			WithExec([]string{"sh", "-c", "sleep 9999"}).
			Sync(ctx)
		if errors.Is(err, cancelErr) {
			return nil
		}
		return err
	})

	return func() error {
		cancel(cancelErr)
		return eg.Wait()
	}
}

// requireErrOut is the same as require.ErrorContains, except it also looks in
// the Stdout/Stderr of a *dagger.ExecErr, since that's something we do a lot
// in tests.
//
// TODO: A better alternative might be to record the log output and assert
// against what the user sees there, but that's a bigger lift.
func requireErrOut(t *testctx.T, err error, out string) {
	if err == nil {
		require.Fail(t, "expected error, got nil")
	}
	var execErr *dagger.ExecError
	if errors.As(err, &execErr) {
		require.Contains(
			t,
			fmt.Sprintf("%s\nStdout: %s\nStderr: %s", err, execErr.Stdout, execErr.Stderr),
			out,
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
	if err == nil {
		require.Fail(t, "expected error, got nil")
	}
	var execErr *dagger.ExecError
	if errors.As(err, &execErr) {
		require.Regexp(
			t,
			re,
			fmt.Sprintf("%s\nStdout: %s\nStderr: %s", err, execErr.Stdout, execErr.Stderr),
		)
		return
	}
	require.Regexp(t, re, err.Error())
}
