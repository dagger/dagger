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
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	// start with fresh test registries once per suite; they're an engine-global
	// dependency
	// startRegistry()
	// startPrivateRegistry()
	os.Exit(m.Run())
}

func connect(t *testing.T) (*dagger.Client, context.Context) {
	tw := newTWriter(t)
	t.Cleanup(tw.Flush)
	return connectWithLogOutput(t, tw)
}

func connectWithBufferedLogs(t *testing.T) (*dagger.Client, context.Context, *safeBuffer) {
	tw := newTWriter(t)
	t.Cleanup(tw.Flush)
	output := &safeBuffer{}
	c, ctx := connectWithLogOutput(t, io.MultiWriter(tw, output))
	return c, ctx, output
}

func connectWithLogOutput(t *testing.T, logOutput io.Writer) (*dagger.Client, context.Context) {
	ctx := context.Background()
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(logOutput))
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })
	return client, ctx
}

func newCache(t *testing.T) core.CacheID {
	var res struct {
		CacheVolume struct {
			ID core.CacheID
		}
	}

	err := testutil.Query(`
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

func newDirWithFile(t *testing.T, path, contents string) core.DirectoryID {
	dirRes := struct {
		Directory struct {
			WithNewFile struct {
				ID core.DirectoryID
			}
		}
	}{}

	err := testutil.Query(
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

func newFile(t *testing.T, path, contents string) core.FileID {
	var secretRes struct {
		Directory struct {
			WithNewFile struct {
				File struct {
					ID core.FileID
				}
			}
		}
	}

	err := testutil.Query(
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

func tarEntries(t *testing.T, path string) []string {
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

func readTarFile(t *testing.T, pathToTar, pathInTar string) []byte {
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

func checkNotDisabled(t *testing.T, env string) { //nolint:unparam
	if os.Getenv(env) == "0" {
		t.Skipf("disabled via %s=0", env)
	}
}

func computeMD5FromReader(reader io.Reader) string {
	h := md5.New()
	io.Copy(h, reader)
	return fmt.Sprintf("%x", h.Sum(nil))
}

func daggerCliPath(t *testing.T) string {
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

func daggerCliFile(t *testing.T, c *dagger.Client) *dagger.File {
	t.Helper()
	return c.Host().File(daggerCliPath(t))
}

const testCLIBinPath = "/bin/dagger"

func CLITestContainer(ctx context.Context, t *testing.T, c *dagger.Client) *DaggerCLIContainer {
	t.Helper()
	ctr := c.Container().From(alpineImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

	return &DaggerCLIContainer{
		Container: ctr,
		ctx:       ctx,
		t:         t,
		c:         c,
	}
}

type DaggerCLIContainer struct {
	*dagger.Container
	ctx context.Context
	t   *testing.T
	c   *dagger.Client

	// common
	EnvArg string

	// "env init"
	SDKArg  string
	NameArg string
	RootArg string
}

const cliContainerRepoMntPath = "/src"

func (ctr DaggerCLIContainer) WithLoadedEnv(
	environmentPath string,
	convertToGitEnv bool,
) *DaggerCLIContainer {
	ctr.t.Helper()
	thisRepoPath, err := filepath.Abs("../..")
	require.NoError(ctr.t, err)

	thisRepoDir := ctr.c.Host().Directory(thisRepoPath, dagger.HostDirectoryOpts{
		Include: []string{"core", "sdk", "go.mod", "go.sum"},
	})
	environmentArg := filepath.Join(cliContainerRepoMntPath, environmentPath)

	baseCtr := ctr.Container
	if convertToGitEnv {
		branchName := identity.NewID()
		gitSvc, _ := gitServiceWithBranch(ctr.ctx, ctr.t, ctr.c, thisRepoDir, branchName)
		baseCtr = baseCtr.WithServiceBinding("git", gitSvc)

		endpoint, err := gitSvc.Endpoint(ctr.ctx)
		require.NoError(ctr.t, err)
		environmentArg = "git://" + endpoint + "/repo.git" + "?ref=" + branchName + "&protocol=git"
		if environmentPath != "" {
			environmentArg += "&subpath=" + environmentPath
		}
	} else {
		baseCtr = baseCtr.WithMountedDirectory(cliContainerRepoMntPath, thisRepoDir)
	}

	ctr.Container = baseCtr
	ctr.EnvArg = environmentArg
	return &ctr
}

func (ctr DaggerCLIContainer) WithEnvArg(environmentArg string) *DaggerCLIContainer {
	ctr.EnvArg = environmentArg
	return &ctr
}

func (ctr DaggerCLIContainer) WithSDKArg(sdk string) *DaggerCLIContainer {
	ctr.SDKArg = sdk
	return &ctr
}

func (ctr DaggerCLIContainer) WithNameArg(name string) *DaggerCLIContainer {
	ctr.NameArg = name
	return &ctr
}

func (ctr DaggerCLIContainer) CallChecks(selectedChecks ...string) *DaggerCLIContainer {
	args := []string{testCLIBinPath, "--debug", "--progress=plain"}
	if ctr.EnvArg != "" {
		args = append(args, "--env", ctr.EnvArg)
	}
	args = append(args, "checks")
	if len(selectedChecks) > 0 {
		args = append(args, selectedChecks...)
	}
	ctr.Container = ctr.Container.WithExec(args, dagger.ContainerWithExecOpts{ExperimentalPrivilegedNesting: true})
	return &ctr
}

func (ctr DaggerCLIContainer) CallEnv() *DaggerCLIContainer {
	args := []string{testCLIBinPath, "env"}
	if ctr.EnvArg != "" {
		args = append(args, "--env", ctr.EnvArg)
	}
	ctr.Container = ctr.WithExec(args, dagger.ContainerWithExecOpts{ExperimentalPrivilegedNesting: true})
	return &ctr
}

func (ctr DaggerCLIContainer) CallEnvInit() *DaggerCLIContainer {
	args := []string{testCLIBinPath, "env", "init"}
	if ctr.EnvArg != "" {
		args = append(args, "--env", ctr.EnvArg)
	}
	if ctr.SDKArg != "" {
		args = append(args, "--sdk", ctr.SDKArg)
	}
	if ctr.NameArg != "" {
		args = append(args, "--name", ctr.NameArg)
	}
	if ctr.RootArg != "" {
		args = append(args, "--root", ctr.RootArg)
	}
	ctr.Container = ctr.WithExec(args, dagger.ContainerWithExecOpts{ExperimentalPrivilegedNesting: true})
	return &ctr
}

func goCache(c *dagger.Client) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.
			WithMountedCache("/go/pkg/mod", c.CacheVolume("go-mod")).
			WithEnvVariable("GOMODCACHE", "/go/pkg/mod").
			WithMountedCache("/go/build-cache", c.CacheVolume("go-build")).
			WithEnvVariable("GOCACHE", "/go/build-cache")
	}
}

// tWriter is a writer that writes to testing.T
type tWriter struct {
	t   *testing.T
	buf bytes.Buffer
	mu  sync.Mutex
}

// newTWriter creates a new TWriter
func newTWriter(t *testing.T) *tWriter {
	return &tWriter{t: t}
}

// Write writes data to the testing.T
func (tw *tWriter) Write(p []byte) (n int, err error) {
	tw.mu.Lock()
	defer tw.mu.Unlock()

	tw.t.Helper()

	if n, err = tw.buf.Write(p); err != nil {
		return n, err
	}

	for {
		line, err := tw.buf.ReadBytes('\n')
		if err == io.EOF {
			// If we've reached the end of the buffer, write it back, because it doesn't have a newline
			tw.buf.Write(line)
			break
		}
		if err != nil {
			return n, err
		}

		tw.t.Log(strings.TrimSuffix(string(line), "\n"))
	}
	return n, nil
}

func (tw *tWriter) Flush() {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	tw.t.Log(tw.buf.String())
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
