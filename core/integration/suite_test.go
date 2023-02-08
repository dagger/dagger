package core

import (
	"archive/tar"
	"context"
	"errors"
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
)

func startRegistry(t *testing.T) {
	t.Helper()

	if err := exec.Command("docker", "inspect", registryContainer).Run(); err != nil {
		// start registry if it doesn't exist
		runCmd(t, "docker", "rm", "-f", registryContainer)
		runCmd(t, "docker", "run", "--rm", "--name", registryContainer, "--net", "container:"+engineContainer, "-d", "registry:2")
	}

	runCmd(t, "docker", "exec", engineContainer, "sh", "-c", "for i in $(seq 1 60); do nc -zv 127.0.0.1 5000 && exit 0; sleep 1; done; exit 1")
}

func startPrivateRegistry(ctx context.Context, c *dagger.Client, t *testing.T) {
	t.Helper()

	authDir := t.TempDir()
	require.NoError(t,
		os.WriteFile(
			filepath.Join(authDir, "htpasswd"),
			// Plaintext = john:xFlejaPdjrt25Dvr
			[]byte("john:$2y$05$/iP8ud0Fs8o3NLlElyfVVOp6LesJl3oRLYoc3neArZKWX10OhynSC"),
			0600,
		),
	)

	if err := exec.Command("docker", "inspect", privateRegistryContainer).Run(); err != nil {
		// start registry if it doesn't exist
		runCmd(t, "docker", "rm", "-f", privateRegistryContainer)
		runCmd(t, "docker", "run", "--rm",
			"--name", privateRegistryContainer,
			"--net", "container:"+engineContainer,
			"-e", "REGISTRY_HTTP_ADDR=127.0.0.1:5010",
			"-e", "REGISTRY_AUTH=htpasswd",
			"-e", "REGISTRY_AUTH_HTPASSWD_REALM=Registry Realm",
			"-e", "REGISTRY_AUTH_HTPASSWD_PATH=/auth/htpasswd",
			"-v", authDir+":/auth",
			"-d",
			"registry:2",
		)
	}

	runCmd(t, "docker", "exec", engineContainer,
		"sh", "-c",
		"for i in $(seq 1 60); do nc -zv 127.0.0.1 5010 && exit 0; sleep 1; done; exit 1")
}

func runCmd(t *testing.T, exe string, args ...string) {
	t.Helper()

	t.Logf("running %s %s", exe, strings.Join(args, " "))

	cmd := exec.Command(exe, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run())
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
