package core

import (
	"archive/tar"
	"context"
	"crypto/md5"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	os.Setenv("_DAGGER_DEBUG_HEALTHCHECKS", "1")
	// start with fresh test registries once per suite; they're an engine-global
	// dependency
	// startRegistry()
	// startPrivateRegistry()
	os.Exit(m.Run())
}

func connect(t require.TestingT) (*dagger.Client, context.Context) {
	ctx := context.Background()
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	require.NoError(t, err)
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

func newSecret(t *testing.T, content string) core.SecretID {
	var secretRes struct {
		Directory struct {
			WithNewFile struct {
				File struct {
					Secret struct {
						ID core.SecretID
					}
				}
			}
		}
	}

	err := testutil.Query(
		`query Test($content: String!) {
			directory {
				withNewFile(path: "some-file", contents: $content) {
					file(path: "some-file") {
						secret {
							id
						}
					}
				}
			}
		}`, &secretRes, &testutil.QueryOptions{Variables: map[string]any{
			"content": content,
		}})
	require.NoError(t, err)

	secretID := secretRes.Directory.WithNewFile.File.Secret.ID
	require.NotEmpty(t, secretID)

	return secretID
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

func lastNLines(str string, n int) string {
	lines := strings.Split(strings.TrimSpace(str), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

const testCLIBinPath = "/bin/dagger"

func CLITestContainer(ctx context.Context, t *testing.T, c *dagger.Client) *DaggerCLIContainer {
	t.Helper()
	ctr := c.Container().From(alpineImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", testCLIBinPath).
		// TODO: this shouldn't be needed, dagger cli should pick up existing nestedness
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", "unix:///.runner.sock")

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
	ProjectArg string

	// "do"
	OutputArg string
	TargetArg string
	UserArgs  map[string]string

	// "project init"
	SDKArg  string
	NameArg string
	RootArg string
}

const cliContainerRepoMntPath = "/src"

func (ctr DaggerCLIContainer) WithLoadedProject(
	projectPath string,
	convertToGitProject bool,
) *DaggerCLIContainer {
	ctr.t.Helper()
	thisRepoPath, err := filepath.Abs("../..")
	require.NoError(ctr.t, err)

	thisRepoDir := ctr.c.Host().Directory(thisRepoPath, dagger.HostDirectoryOpts{
		Include: []string{"core", "sdk", "go.mod", "go.sum"},
	})
	projectArg := filepath.Join(cliContainerRepoMntPath, projectPath)

	baseCtr := ctr.Container
	if convertToGitProject {
		gitSvc, _ := gitService(ctr.ctx, ctr.t, ctr.c, thisRepoDir)
		baseCtr = baseCtr.WithServiceBinding("git", gitSvc)

		endpoint, err := gitSvc.Endpoint(ctr.ctx)
		require.NoError(ctr.t, err)
		projectArg = "git://" + endpoint + "/repo.git" + "?ref=main&protocol=git"
		if projectPath != "" {
			projectArg += "&subpath=" + projectPath
		}
	} else {
		baseCtr = baseCtr.WithMountedDirectory(cliContainerRepoMntPath, thisRepoDir)
	}

	ctr.Container = baseCtr
	ctr.ProjectArg = projectArg
	return &ctr
}

func (ctr DaggerCLIContainer) WithProjectArg(projectArg string) *DaggerCLIContainer {
	ctr.ProjectArg = projectArg
	return &ctr
}

func (ctr DaggerCLIContainer) WithOutputArg(outputArg string) *DaggerCLIContainer {
	ctr.OutputArg = outputArg
	return &ctr
}

func (ctr DaggerCLIContainer) WithTarget(target string) *DaggerCLIContainer {
	ctr.TargetArg = target
	return &ctr
}

func (ctr DaggerCLIContainer) WithUserArg(key, value string) *DaggerCLIContainer {
	if ctr.UserArgs == nil {
		ctr.UserArgs = map[string]string{}
	}
	ctr.UserArgs[key] = value
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

func (ctr DaggerCLIContainer) CallDo() *DaggerCLIContainer {
	args := []string{testCLIBinPath, "do"}
	if ctr.ProjectArg != "" {
		args = append(args, "--project", ctr.ProjectArg)
	}
	if ctr.OutputArg != "" {
		args = append(args, "--output", ctr.OutputArg)
	}
	args = append(args, ctr.TargetArg)
	for k, v := range ctr.UserArgs {
		args = append(args, "--"+k, v)
	}
	ctr.Container = ctr.Container.WithExec(args, dagger.ContainerWithExecOpts{ExperimentalPrivilegedNesting: true})
	return &ctr
}

func (ctr DaggerCLIContainer) CallProject() *DaggerCLIContainer {
	args := []string{testCLIBinPath, "--silent", "project"}
	if ctr.ProjectArg != "" {
		args = append(args, "--project", ctr.ProjectArg)
	}
	ctr.Container = ctr.WithExec(args, dagger.ContainerWithExecOpts{ExperimentalPrivilegedNesting: true})
	return &ctr
}

func (ctr DaggerCLIContainer) CallProjectInit() *DaggerCLIContainer {
	args := []string{testCLIBinPath, "--silent", "project", "init"}
	if ctr.ProjectArg != "" {
		args = append(args, "--project", ctr.ProjectArg)
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
