package core

import (
	"context"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestSecretEnvFromFile(t *testing.T) {
	t.Parallel()

	secretID := newSecret(t, "some-content")

	var envRes struct {
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

	err := testutil.Query(
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
		}`, &envRes, &testutil.QueryOptions{Variables: map[string]any{
			"secret": secretID,
		}})
	require.NoError(t, err)
	require.Contains(t, envRes.Container.From.WithSecretVariable.Exec.Stdout.Contents, "SECRET=some-content\n")
}

func TestSecretMountFromFile(t *testing.T) {
	t.Parallel()

	secretID := newSecret(t, "some-content")

	var envRes struct {
		Container struct {
			From struct {
				WithMountedSecret struct {
					Exec struct {
						Stdout struct{ Contents string }
					}
				}
			}
		}
	}

	err := testutil.Query(
		`query Test($secret: SecretID!) {
			container {
				from(address: "alpine:3.16.2") {
					withMountedSecret(path: "/sekret", source: $secret) {
						exec(args: ["cat", "/sekret"]) {
							stdout { contents }
						}
					}
				}
			}
		}`, &envRes, &testutil.QueryOptions{Variables: map[string]any{
			"secret": secretID,
		}})
	require.NoError(t, err)
	require.Contains(t, envRes.Container.From.WithMountedSecret.Exec.Stdout.Contents, "some-content")
}

func TestSecretMountFromFileWithOverridingMount(t *testing.T) {
	t.Parallel()

	secretID := newSecret(t, "some-secret")
	fileID := newFile(t, "some-file", "some-content")

	var res struct {
		Container struct {
			From struct {
				WithMountedSecret struct {
					WithMountedFile struct {
						Exec struct {
							Stdout struct{ Contents string }
						}
						File struct {
							Contents string
						}
					}
				}
			}
		}
	}

	err := testutil.Query(
		`query Test($secret: SecretID!, $file: FileID!) {
			container {
				from(address: "alpine:3.16.2") {
					withMountedSecret(path: "/sekret", source: $secret) {
						withMountedFile(path: "/sekret", source: $file) {
							exec(args: ["cat", "/sekret"]) {
								stdout { contents }
							}
							file(path: "/sekret") {
								contents
							}
						}
					}
				}
			}
		}`, &res, &testutil.QueryOptions{Variables: map[string]any{
			"secret": secretID,
			"file":   fileID,
		}})
	require.NoError(t, err)
	require.Contains(t, res.Container.From.WithMountedSecret.WithMountedFile.Exec.Stdout.Contents, "some-secret")
	require.Contains(t, res.Container.From.WithMountedSecret.WithMountedFile.File.Contents, "some-content")
}

func TestSecretPlaintext(t *testing.T) {
	ctx := context.Background()
	c, err := dagger.Connect(ctx)
	require.NoError(t, err)
	defer c.Close()

	plaintext, err := c.Directory().
		WithNewFile("TOP_SECRET", dagger.DirectoryWithNewFileOpts{Contents: "hi"}).File("TOP_SECRET").Secret().Plaintext(ctx)
	require.NoError(t, err)
	require.Equal(t, "hi", plaintext)
}
