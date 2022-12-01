package core

import (
	"archive/tar"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"
)

func connect(t *testing.T) (*dagger.Client, context.Context) {
	ctx := context.Background()
	client, err := dagger.Connect(ctx)
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

func startRegistry(ctx context.Context, c *dagger.Client, t *testing.T) {
	t.Helper()

	// include a random ID so it runs every time (hack until we have no-cache or equivalent support)
	randomID := identity.NewID()
	go func() {
		_, err := c.Container().
			From("registry:2").
			WithEnvVariable("RANDOM", randomID).
			WithExec([]string{}).
			ExitCode(ctx)
		if err != nil {
			t.Logf("error running registry: %v", err)
		}
	}()

	_, err := c.Container().
		From("alpine:3.16.2").
		WithEnvVariable("RANDOM", randomID).
		WithExec([]string{"sh", "-c", "for i in $(seq 1 60); do nc -zv 127.0.0.1 5000 && exit 0; sleep 1; done; exit 1"}).
		ExitCode(ctx)
	require.NoError(t, err)
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

func echoServer(t *testing.T, network, addr string) net.Listener {
	l, err := net.Listen(network, addr)
	require.NoError(t, err)

	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				if !errors.Is(err, net.ErrClosed) {
					t.Logf("accept: %s", err)
					panic(err)
				}
				return
			}

			n, err := io.Copy(c, c)
			if err != nil {
				t.Logf("hello: %s", err)
				panic(err)
			}

			t.Logf("copied %d bytes", n)

			err = c.Close()
			if err != nil {
				t.Logf("close: %s", err)
				panic(err)
			}
		}
	}()

	return l
}

func echoServerUDP(t *testing.T, addr string) *net.UDPConn {
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	require.NoError(t, err)

	conn, err := net.ListenUDP("udp", udpAddr)
	require.NoError(t, err)

	go func() {
		for {
			buf := make([]byte, 1024)
			n, remoteAddr, err := conn.ReadFromUDP(buf)
			if err != nil {
				if !errors.Is(err, net.ErrClosed) {
					t.Logf("accept: %s", err)
					panic(err)
				}
				return
			}

			msg := buf[0:n]

			n, err = conn.WriteToUDP(msg, remoteAddr)
			if err != nil {
				t.Logf("hello: %s", err)
				panic(err)
			}

			t.Logf("copied %d bytes", n)
		}
	}()

	return conn
}
