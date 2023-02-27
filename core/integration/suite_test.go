package core

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

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
	startRegistry()
	startPrivateRegistry()
	os.Exit(m.Run())
}

func connect(t *testing.T) (*dagger.Client, context.Context) {
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
	registryContainer        = "dagger-registry.dev"
	privateRegistryContainer = "dagger-private-registry.dev"
	engineContainer          = "dagger-engine.dev"

	registryHost        = "127.0.0.1:5000"
	privateRegistryHost = "127.0.0.1:5010"
)

func startRegistry() {
	runCmd("docker", "rm", "-f", registryContainer)
	runCmd("docker", "run", "--rm", "--name", registryContainer, "--net", "container:"+engineContainer, "-d", "registry:2")

	waitForRegistry(registryHost)
}

func startPrivateRegistry() {
	// john:xFlejaPdjrt25Dvr
	const htpasswd = "john:$2y$05$/iP8ud0Fs8o3NLlElyfVVOp6LesJl3oRLYoc3neArZKWX10OhynSC" //nolint:gosec

	// start registry if it doesn't exist
	runCmd("docker", "rm", "-f", privateRegistryContainer)
	runCmd("docker", "run", "--rm",
		"--name", privateRegistryContainer,
		"--net", "container:"+engineContainer,
		"--env", "REGISTRY_HTTP_ADDR="+privateRegistryHost,
		"--env", "REGISTRY_AUTH=htpasswd",
		"--env", "REGISTRY_AUTH_HTPASSWD_REALM=Registry Realm",
		"--env", "REGISTRY_AUTH_HTPASSWD_PATH=/auth/htpasswd",
		"--env", "REGISTRY_HTPASSWD="+htpasswd,
		"--entrypoint", "",
		"-d",
		"registry:2",
		"sh", "-exc", `mkdir -p /auth && echo "$REGISTRY_HTPASSWD" > /auth/htpasswd && /entrypoint.sh /etc/docker/registry/config.yml`,
	)

	waitForRegistry(privateRegistryHost)
}

func registryRef(name string) string {
	return fmt.Sprintf("%s/%s:%s", registryHost, name, identity.NewID())
}

func privateRegistryRef(name string) string {
	return fmt.Sprintf("%s/%s:%s", privateRegistryHost, name, identity.NewID())
}

func waitForRegistry(addr string) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		panic(err)
	}

	start := time.Now()
	for i := 0; i < 100; i++ {
		cmd := exec.Command("docker", "exec", engineContainer, "nc", "-zv", host, port)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if err := cmd.Run(); err == nil {
			break
		}

		if i == 99 {
			log.Println("registry", addr, "not ready after", time.Since(start))
			os.Exit(1)
		}

		time.Sleep(100 * time.Millisecond)
	}
}

func runCmd(exe string, args ...string) { //nolint:unparam
	fmt.Printf("running %s %s", exe, strings.Join(args, " "))

	cmd := exec.Command(exe, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err != nil {
		os.Exit(cmd.ProcessState.ExitCode())
	}
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

func checkEnabled(t *testing.T, env string) {
	if os.Getenv(env) == "" {
		t.Skipf("set $%s to enable", env)
	}
}
