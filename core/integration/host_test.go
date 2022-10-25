package core

import (
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
				Read struct {
					ID core.DirectoryID
				}
			}
		}
	}

	err := testutil.Query(
		`{
			host {
				workdir {
					read {
						id
					}
				}
			}
		}`, &secretRes, nil)
	require.NoError(t, err)

	hostRes := secretRes.Host.Workdir.Read.ID
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

	// FIXME(vito): this is brittle; it currently finds the README in the root of
	// the repo but it'd be better to control the workdir
	require.Contains(t, execRes.Container.From.WithMountedDirectory.Exec.Stdout.Contents, "suite_test.go")
}

func TestHostLocalDirReadWrite(t *testing.T) {
	t.Parallel()

	dir1 := t.TempDir()
	err := os.WriteFile(path.Join(dir1, "foo"), []byte("bar"), 0600)
	require.NoError(t, err)

	dir2 := t.TempDir()

	var readRes struct {
		Host struct {
			Directory struct {
				Read struct {
					ID core.DirectoryID
				}
			}
		}
	}

	err = testutil.Query(
		`{
			host {
				directory(id: "dir1") {
					read {
						id
					}
				}
			}
		}`, &readRes, nil, dagger.WithLocalDir("dir", dir1))
	require.NoError(t, err)

	srcID := readRes.Host.Directory.Read.ID

	var writeRes struct {
		Host struct {
			Directory struct {
				Write bool
			}
		}
	}

	err = testutil.Query(
		`query Test($src: DirectoryID!) {
			host {
				directory(id: "dir2") {
					write(contents: $src)
				}
			}
		}`, &writeRes, &testutil.QueryOptions{
			Variables: map[string]any{
				"src": srcID,
			},
		},
		dagger.WithLocalDir("dir1", dir1),
		dagger.WithLocalDir("dir2", dir2),
	)
	require.NoError(t, err)

	require.True(t, writeRes.Host.Directory.Write)

	content, err := os.ReadFile(path.Join(dir2, "foo"))
	require.NoError(t, err)
	require.Equal(t, "bar", string(content))
}

func TestHostLocalDirWrite(t *testing.T) {
	t.Parallel()

	dir1 := t.TempDir()

	var contentRes struct {
		Directory struct {
			WithNewFile struct {
				ID core.DirectoryID
			}
		}
	}

	err := testutil.Query(
		`{
			directory {
				withNewFile(path: "foo", contents: "bar") {
					id
				}
			}
		}`, &contentRes, nil)
	require.NoError(t, err)

	srcID := contentRes.Directory.WithNewFile.ID

	var writeRes struct {
		Host struct {
			Directory struct {
				Write bool
			}
		}
	}

	err = testutil.Query(
		`query Test($src: DirectoryID!) {
			host {
				directory(id: "dir1") {
					write(contents: $src)
				}
			}
		}`, &writeRes, &testutil.QueryOptions{
			Variables: map[string]any{
				"src": srcID,
			},
		},
		dagger.WithLocalDir("dir1", dir1),
		dagger.WithLocalDir("dir2", dir1),
	)
	require.NoError(t, err)

	require.True(t, writeRes.Host.Directory.Write)

	content, err := os.ReadFile(path.Join(dir1, "foo"))
	require.NoError(t, err)
	require.Equal(t, "bar", string(content))
}

func TestHostVariable(t *testing.T) {
	t.Parallel()

	var secretRes struct {
		Host struct {
			EnvVariable struct {
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
				envVariable(name: "HELLO_TEST") {
					value
					secret {
						id
					}
				}
			}
		}`, &secretRes, nil)
	require.NoError(t, err)

	varValue := secretRes.Host.EnvVariable.Value
	require.Equal(t, "hello", varValue)

	varSecret := secretRes.Host.EnvVariable.Secret.ID

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
