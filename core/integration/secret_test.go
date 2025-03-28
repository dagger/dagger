package core

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"io"
	"testing"

	"dagger.io/dagger"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
)

type SecretSuite struct{}

func TestSecret(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(SecretSuite{})
}

func (SecretSuite) TestEnvFromFile(ctx context.Context, t *testctx.T) {
	err := testutil.Query(t,
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
		}`, nil, &testutil.QueryOptions{Secrets: map[string]string{
			"secret": "some-content",
		}})
	require.NoError(t, err)
}

func (SecretSuite) TestMountFromFile(ctx context.Context, t *testctx.T) {
	err := testutil.Query(t,
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
		}`, nil, &testutil.QueryOptions{Secrets: map[string]string{
			"secret": "some-content",
		}})
	require.NoError(t, err)
}

func (SecretSuite) TestMountFromFileWithOverridingMount(ctx context.Context, t *testctx.T) {
	plaintext := "some-secret"
	fileID := newFile(t, "some-file", "some-content")

	var res struct {
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
	}

	err := testutil.Query(t,
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
		}`, &res, &testutil.QueryOptions{
			Variables: map[string]any{
				"file": fileID,
			},
			Secrets: map[string]string{
				"secret": plaintext,
			},
		},
	)
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

//go:embed testdata/secretkey.txt
var secretKeyBytes []byte

func (SecretSuite) TestVaultSecretsProviderTTL(ctx context.Context, t *testctx.T) {
	var baseContainer = func(c *dagger.Client, vault *dagger.Service) *dagger.Container {
		return c.Container().
			From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithFile("/bin/vault", c.Container().From("hashicorp/vault").File("/bin/vault")).
			WithEnvVariable("VAULT_ADDR", "http://vault:8200").
			WithEnvVariable("VAULT_TOKEN", "vault-root-token").
			WithServiceBinding("vault", vault)
	}

	var verifySecretFromVault = func(ctx context.Context, c *dagger.Client, vault *dagger.Service, secretURL string) (string, error) {
		return baseContainer(c, vault).
			WithWorkdir("/work").
			With(daggerExec("init", "--sdk=go", "--name=foo", "--source=.")).
			WithNewFile("main.go", `package main

import (
	"context"
	"dagger/foo/internal/dagger"
	"fmt"
	"time"
)

type Foo struct{}


// This function sets the value of secret in vault, gets its plaintext value. Then
// this function updates the value of secret in vault (to simulate expired or changed secret), and sleeps for 20s to allow for ttl (if any) 
// to expire. It then gets its plaintext value again.
// After that it returns both the values as string, which our tescase then verifies.
func (m *Foo) VerifySecret(ctx context.Context, vault *dagger.Service, secret *dagger.Secret) (string, error) {
	_, err := dag.Container().From("hashicorp/vault").
		WithEnvVariable("VAULT_ADDR", "http://vault:8200").
		WithEnvVariable("VAULT_TOKEN", "vault-root-token").
		WithServiceBinding("vault", vault).
		WithExec([]string{"sh", "-c", "vault kv put secret/my-secret username=\"admin\" password=\"original-password\""}).Sync(ctx)
	if err != nil {
		return "", err
	}

	original, err := secret.Plaintext(ctx)
	if err != nil {
		return "", err
	}

	// simulate an update in secret while the pipeline is running
	_, err = dag.Container().From("hashicorp/vault").
		WithEnvVariable("VAULT_ADDR", "http://vault:8200").
		WithEnvVariable("VAULT_TOKEN", "vault-root-token").
		WithServiceBinding("vault", vault).
		WithExec([]string{"sh", "-c", "vault kv put secret/my-secret username=\"admin\" password=\"updated-password\""}).Sync(ctx)
	if err != nil {
		return "", err
	}

	// wait for ttl to expire, to simulate a long running process
	time.Sleep(20 * time.Second)

	// now the pipeline needs secret again.
	updated, err := secret.Plaintext(ctx)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("original: %s\nupdated: %s", original, updated), nil
}
`).
			With(daggerCall("-vvv", "verify-secret", fmt.Sprintf("--secret=%s", secretURL), "--vault=tcp://vault:8200")).Stdout(ctx)
	}

	testcases := []struct {
		name                    string
		secret                  string
		expectedUpdatedPassword string
	}{
		{
			name:                    "without any ttl it caches forever",
			secret:                  "vault://data/my-secret.password",
			expectedUpdatedPassword: "original-password",
		},
		{
			name:                    "respect ttl if provided",
			secret:                  "vault://data/my-secret.password?ttl=10s",
			expectedUpdatedPassword: "updated-password",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			vault := c.Container().
				From("hashicorp/vault").
				WithEnvVariable("VAULT_DEV_ROOT_TOKEN_ID", "vault-root-token").
				WithEnvVariable("VAULT_LOG_LEVEL", "debug").
				WithExposedPort(8200).
				AsService(dagger.ContainerAsServiceOpts{
					UseEntrypoint:                 true,
					ExperimentalPrivilegedNesting: true,
					InsecureRootCapabilities:      true,
				})

			output, err := verifySecretFromVault(ctx, c, vault, tc.secret)
			require.NoError(t, err)
			require.Equal(t, fmt.Sprintf("original: original-password\nupdated: %s", tc.expectedUpdatedPassword), output)
		})
	}
}
