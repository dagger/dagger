package core

import (
	"bytes"
	"compress/gzip"
	"context"
	_ "embed"
	"io"
	"strings"
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
						ExitCode int
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
						exec(args: ["sh", "-c", "test \"$SECRET\" = \"some-content\""]) {
							exitCode
						}
					}
				}
			}
		}`, &envRes, &testutil.QueryOptions{Variables: map[string]any{
			"secret": secretID,
		}})
	require.NoError(t, err)
	require.Equal(t, 0, envRes.Container.From.WithSecretVariable.Exec.ExitCode)
}

func TestSecretMountFromFile(t *testing.T) {
	t.Parallel()

	secretID := newSecret(t, "some-content")

	var envRes struct {
		Container struct {
			From struct {
				WithMountedSecret struct {
					Exec struct {
						ExitCode int
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
						exec(args: ["sh", "-c", "test \"$(cat /sekret)\" = \"some-content\""]) {
							exitCode
						}
					}
				}
			}
		}`, &envRes, &testutil.QueryOptions{Variables: map[string]any{
			"secret": secretID,
		}})
	require.NoError(t, err)
	require.Equal(t, 0, envRes.Container.From.WithMountedSecret.Exec.ExitCode)
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
							ExitCode int
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
							exec(args: ["sh", "-c", "test \"$(cat /sekret)\" = \"some-secret\""]) {
								exitCode
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
	require.Equal(t, 0, res.Container.From.WithMountedSecret.WithMountedFile.Exec.ExitCode)
	require.Contains(t, res.Container.From.WithMountedSecret.WithMountedFile.File.Contents, "some-content")
}

func TestSecretPlaintext(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c, err := dagger.Connect(ctx)
	require.NoError(t, err)
	defer c.Close()

	//nolint:staticcheck // SA1019 We want to test this API while we support it.
	plaintext, err := c.Directory().
		WithNewFile("TOP_SECRET", "hi").File("TOP_SECRET").Secret().Plaintext(ctx)
	require.NoError(t, err)
	require.Equal(t, "hi", plaintext)
}

func TestNewSecret(t *testing.T) {
	t.Parallel()
	c, ctx := connect(t)
	defer c.Close()

	secretValue := "very-secret-text"

	s := c.SetSecret("aws_key", secretValue)

	exitCode, err := c.Container().From("alpine:3.16.2").
		WithSecretVariable("AWS_KEY", s).
		WithExec([]string{"sh", "-c", "test \"$AWS_KEY\" = \"very-secret-text\""}).
		ExitCode(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, exitCode)
}

func TestWhitespaceSecretScrubbed(t *testing.T) {
	t.Parallel()
	c, ctx := connect(t)
	defer c.Close()

	secretValue := "very\nsecret\ntext\n"

	s := c.SetSecret("aws_key", secretValue)

	stdout, err := c.Container().From("alpine:3.16.2").
		WithSecretVariable("AWS_KEY", s).
		WithExec([]string{"sh", "-c", "test \"$AWS_KEY\" = \"very\nsecret\ntext\n\""}).
		WithExec([]string{"sh", "-c", "echo  -n \"$AWS_KEY\""}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "***\n***\n***\n", stdout)
}

func TestBigWhitespaceSecretScrubbed(t *testing.T) {
	c, ctx := connect(t)
	defer c.Close()

	t.Log("cert size:", len(gzippedLoremBytes))

	loremReader, err := gzip.NewReader(bytes.NewReader(gzippedLoremBytes))
	require.NoError(t, err)

	secretValue, err := io.ReadAll(loremReader)
	require.NoError(t, err)

	s := c.SetSecret("aws_key", string(secretValue))

	sec := c.Container().From("alpine:3.16.2").
		WithSecretVariable("AWS_KEY", s).
		WithExec([]string{"sh", "-c", "echo  -n \"$AWS_KEY\""})

	stdout, err := sec.Stdout(ctx)
	require.NoError(t, err)
	paragraphCount := strings.Count(string(secretValue), "\n\n")
	t.Log("pcount:", paragraphCount)
	scrubbedText := strings.Repeat("*** \n\n", paragraphCount)
	scrubbedText += "*** \n"
	require.Equal(t, scrubbedText, stdout)
}

//go:embed testdata/lorem.txt.gz
var gzippedLoremBytes []byte
