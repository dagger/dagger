package core

import (
	"bytes"
	"context"
	_ "embed"
	"io"
	"testing"

	dagger "github.com/dagger/dagger/internal/testutil/dagger"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/testctx"
)

//go:embed testdata/secretkey.txt
var secretKeyBytes []byte

type SecretSuite struct{}

func TestSecret(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(SecretSuite{})
}

func (SecretSuite) TestEnvFromFile(ctx context.Context, t *testctx.T) {
	_, err := Query[any](t,
		`query Test($secret: SecretID!) {
			container {
				from(address: "`+alpineImage+`") {
					withSecretVariable(name: "SECRET", secret: $secret) {
						withExec(args: ["sh", "-c", "test \"$SECRET\" = \"some-content\""]) {
							sync
						}
					}
				}
			}
		}`, &QueryOptions{Secrets: map[string]string{
			"secret": "some-content",
		}})
	require.NoError(t, err)
}

func (SecretSuite) TestMountFromFile(ctx context.Context, t *testctx.T) {
	_, err := Query[any](t,
		`query Test($secret: SecretID!) {
			container {
				from(address: "`+alpineImage+`") {
					withMountedSecret(path: "/sekret", source: $secret) {
						withExec(args: ["sh", "-c", "test \"$(cat /sekret)\" = \"some-content\""]) {
							sync
						}
					}
				}
			}
		}`, &QueryOptions{Secrets: map[string]string{
			"secret": "some-content",
		}})
	require.NoError(t, err)
}

func (SecretSuite) TestMountFromFileWithOverridingMount(ctx context.Context, t *testctx.T) {
	plaintext := "some-secret"
	fileID := newFile(t, "some-file", "some-content")

	res, err := Query[struct {
		Container struct {
			From struct {
				WithMountedSecret struct {
					WithMountedFile struct {
						File struct {
							Contents string
						}
					}
				}
			}
		}
	}](t,
		`query Test($secret: SecretID!, $file: FileID!) {
			container {
				from(address: "`+alpineImage+`") {
					withMountedSecret(path: "/sekret", source: $secret) {
						withMountedFile(path: "/sekret", source: $file) {
							withExec(args: ["sh", "-c", "test \"$(cat /sekret)\" = \"some-secret\""]) {
								sync
							}
							file(path: "/sekret") {
								contents
							}
						}
					}
				}
			}
		}`, &QueryOptions{
			Variables: map[string]any{
				"file": fileID,
			},
			Secrets: map[string]string{
				"secret": plaintext,
			},
		})
	require.NoError(t, err)
	require.Contains(t, res.Container.From.WithMountedSecret.WithMountedFile.File.Contents, "some-content")
}

func (SecretSuite) TestSet(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	secretName := "aws_key"
	secretValue := "very-secret-text"

	s := c.SetSecret(secretName, secretValue)

	ctr, err := c.Container().From(alpineImage).
		WithSecretVariable("AWS_KEY", s).
		WithEnvVariable("word1", "very").
		WithEnvVariable("word2", "secret").
		WithEnvVariable("word3", "text").
		WithExec([]string{"sh", "-exc", "test \"$AWS_KEY\" = \"${word1}-${word2}-${word3}\""}).
		Sync(ctx)
	require.NoError(t, err)

	idEnc, err := ctr.ID(ctx)
	require.NoError(t, err)
	var idp call.ID
	require.NoError(t, idp.Decode(string(idEnc)))
	require.NotContains(t, idp.Display(), secretValue)

	plaintext, err := s.Plaintext(ctx)
	require.NoError(t, err)
	require.Equal(t, secretValue, plaintext)

	name, err := s.Name(ctx)
	require.NoError(t, err)
	require.Equal(t, secretName, name)
}

func (SecretSuite) TestUnsetVariable(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	s := c.SetSecret("aws_key", "very-secret-text")

	out, err := c.Container().
		From(alpineImage).
		WithSecretVariable("AWS_KEY", s).
		WithoutSecretVariable("AWS_KEY").
		WithExec([]string{"printenv"}).
		Stdout(ctx)

	require.NoError(t, err)
	require.NotContains(t, out, "AWS_KEY")
}

func (SecretSuite) TestWhitespaceScrubbed(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	secretValue := "very\nsecret\ntext\n"

	s := c.SetSecret("aws_key", secretValue)

	stdout, err := c.Container().From(alpineImage).
		WithSecretVariable("AWS_KEY", s).
		WithExec([]string{"sh", "-c", "test \"$AWS_KEY\" = \"very\nsecret\ntext\n\""}).
		WithExec([]string{"sh", "-c", "echo -n \"$AWS_KEY\""}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "***", stdout)
}

func (SecretSuite) TestBigScrubbed(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	secretKeyReader := bytes.NewReader(secretKeyBytes)
	secretValue, err := io.ReadAll(secretKeyReader)
	require.NoError(t, err)

	s := c.SetSecret("key", string(secretValue))

	sec := c.Container().From(alpineImage).
		WithSecretVariable("KEY", s).
		WithExec([]string{"sh", "-c", "echo  -n \"$KEY\""})

	stdout, err := sec.Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "***", stdout)
}

func (SecretSuite) TestEmptySecretPlaintext(ctx context.Context, t *testctx.T) {
	callMod := func(c *dagger.Client) (string, error) {
		return goGitBase(t, c).
			WithWorkdir("/work/secreter").
			With(daggerExec("init", "--name=secreter", "--sdk=go", "--source=.")).
			WithNewFile("main.go", `package main

import (
	"context"
	"fmt"

	"dagger/secreter/internal/dagger"
)

type Secreter struct {}

func (*Secreter) CheckEmptyPlaintext(ctx context.Context, s *dagger.Secret) error {
	plaintext, err := s.Plaintext(ctx)
	if err != nil {
		return err
	}
	if plaintext != "" {
		return fmt.Errorf("expected empty plaintext, got %q", plaintext)
	}
	return nil
}
`,
			).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=caller", "--sdk=go", "--source=.")).
			With(daggerExec("install", "./secreter")).
			WithNewFile("main.go", `package main

import (
	"context"
)

type Caller struct {}

func (*Caller) Test(ctx context.Context) error {
	return dag.Secreter().CheckEmptyPlaintext(ctx, dag.SetSecret("FOO", ""))
}
`,
			).
			WithEnvVariable("CACHEBUSTER", identity.NewID()).
			With(daggerCall("test")).
			Stdout(ctx)
	}

	c1 := connect(ctx, t)
	_, err := callMod(c1)
	require.NoError(t, err)
}

func (SecretSuite) TestSetSecretInModuleCaching(ctx context.Context, t *testctx.T) {
	callMod := func(c *dagger.Client) (string, error) {
		return goGitBase(t, c).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
			WithNewFile("main.go", `package main

import (
	"context"
)

type Test struct {}

func (*Test) Fn(ctx context.Context, rand string) (string, error) {
	s := dag.SetSecret("FOO", "bar")
	return dag.Container().From("alpine:3.20").
		WithSecretVariable("FOO", s).
		WithExec([]string{"sh", "-c", "head -c 128 /dev/random | sha256sum"}).
		Stdout(ctx)
}
`,
			).
			With(daggerCall("fn", "--rand", identity.NewID())).
			Stdout(ctx)
	}

	c1 := connect(ctx, t)
	out1, err := callMod(c1)
	require.NoError(t, err)

	c2 := connect(ctx, t)
	out2, err := callMod(c2)
	require.NoError(t, err)

	require.Equal(t, out1, out2)
}
