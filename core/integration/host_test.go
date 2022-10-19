package core

import (
	"context"
	"os"
	"path"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestHostWorkdir(t *testing.T) {
	t.Parallel()

	var secretRes struct {
		Host struct {
			Workdir struct {
				ID core.DirectoryID
			}
		}
	}

	dir := t.TempDir()
	err := os.WriteFile(path.Join(dir, "foo"), []byte("bar"), 0600)
	require.NoError(t, err)

	err = testutil.Query(
		`{
			host {
				workdir {
					id
				}
			}
		}`, &secretRes, nil, dagger.WithWorkdir(dir))
	require.NoError(t, err)

	hostRes := secretRes.Host.Workdir.ID
	require.NotEmpty(t, hostRes)

	var execRes struct {
		Container struct {
			From struct {
				WithMountedDirectory struct {
					Exec struct {
						Stdout struct{ Contents string }
					}
				}
			}
		}
	}

	err = testutil.Query(
		`query Test($host: DirectoryID!) {
			container {
				from(address: "alpine:3.16.2") {
					withMountedDirectory(path: "/host", source: $host) {
						exec(args: ["ls", "/host"]) {
							stdout { contents }
						}
					}
				}
			}
		}`, &execRes, &testutil.QueryOptions{
			Variables: map[string]interface{}{
				"host": hostRes,
			},
		})
	require.NoError(t, err)

	require.Equal(t, "foo\n", execRes.Container.From.WithMountedDirectory.Exec.Stdout.Contents)
}

func TestHostDirectoryRelative(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(path.Join(dir, "some-file"), []byte("hello"), 0600))
	require.NoError(t, os.MkdirAll(path.Join(dir, "some-dir"), 0755))
	require.NoError(t, os.WriteFile(path.Join(dir, "some-dir", "sub-file"), []byte("goodbye"), 0600))

	ctx := context.Background()
	c, err := dagger.Connect(ctx, dagger.WithWorkdir(dir))
	require.NoError(t, err)
	defer c.Close()

	t.Run(". is same as workdir", func(t *testing.T) {
		wdID1, err := c.Core().Host().Directory(".").ID(ctx)
		require.NoError(t, err)

		wdID2, err := c.Core().Host().Workdir().ID(ctx)
		require.NoError(t, err)

		require.Equal(t, wdID1, wdID2)
	})

	t.Run("./foo is relative to workdir", func(t *testing.T) {
		contents, err := c.Core().Host().Directory("some-dir").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"sub-file"}, contents)
	})

	t.Run("../ does not allow escaping", func(t *testing.T) {
		_, err := c.Core().Host().Directory("../").ID(ctx)
		require.Error(t, err)

		// don't reveal the workdir location
		require.NotContains(t, err, dir)
	})
}

func TestHostDirectoryReadWrite(t *testing.T) {
	t.Parallel()

	dir1 := t.TempDir()
	err := os.WriteFile(path.Join(dir1, "foo"), []byte("bar"), 0600)
	require.NoError(t, err)

	dir2 := t.TempDir()

	var readRes struct {
		Host struct {
			Directory struct {
				ID core.DirectoryID
			}
		}
	}

	err = testutil.Query(
		`query Test($dir: String!) {
			host {
				directory(path: $dir) {
					id
				}
			}
		}`, &readRes, &testutil.QueryOptions{
			Variables: map[string]interface{}{
				"dir": dir1,
			},
		})
	require.NoError(t, err)

	srcID := readRes.Host.Directory.ID

	var writeRes struct {
		Directory struct {
			Export bool
		}
	}

	err = testutil.Query(
		`query Test($src: DirectoryID!, $dir2: String!) {
			directory(id: $src) {
				export(path: $dir2)
			}
		}`, &writeRes, &testutil.QueryOptions{
			Variables: map[string]any{
				"src":  srcID,
				"dir2": dir2,
			},
		},
	)
	require.NoError(t, err)

	require.True(t, writeRes.Directory.Export)

	content, err := os.ReadFile(path.Join(dir2, "foo"))
	require.NoError(t, err)
	require.Equal(t, "bar", string(content))
}

func TestHostVariable(t *testing.T) {
	t.Parallel()

	var secretRes struct {
		Host struct {
			Variable struct {
				Value  string
				Secret struct {
					ID core.SecretID
				}
			}
		}
	}

	require.NoError(t, os.Setenv("HELLO_TEST", "hello"))

	err := testutil.Query(
		`{
			host {
				variable(name: "HELLO_TEST") {
					value
					secret {
						id
					}
				}
			}
		}`, &secretRes, nil)
	require.NoError(t, err)

	varValue := secretRes.Host.Variable.Value
	require.Equal(t, "hello", varValue)

	varSecret := secretRes.Host.Variable.Secret.ID

	var execRes struct {
		Container struct {
			From struct {
				WithSecretVariable struct {
					Exec struct {
						Stdout struct{ Contents string }
					}
				}
			}
		}
	}

	err = testutil.Query(
		`query Test($secret: SecretID!) {
			container {
				from(address: "alpine:3.16.2") {
					withSecretVariable(name: "SECRET", secret: $secret) {
						exec(args: ["env"]) {
							stdout { contents }
						}
					}
				}
			}
		}`, &execRes, &testutil.QueryOptions{
			Variables: map[string]interface{}{
				"secret": varSecret,
			},
		})
	require.NoError(t, err)

	require.Contains(t, execRes.Container.From.WithSecretVariable.Exec.Stdout.Contents, "SECRET=hello")
}
