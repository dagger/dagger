package core

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"dagger.io/dagger"
	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"

	"github.com/dagger/testctx"
)

type SecretProvider struct{}

func TestSecretProvider(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(SecretProvider{})
}

func (SecretProvider) TestUnknown(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ctr := c.Container().
		From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

	_, err := fetchSecret(
		ctx,
		ctr,
		"wtf://foobar",
		dagger.ContainerWithExecOpts{ExperimentalPrivilegedNesting: true},
	)
	requireErrOut(t, err, `unsupported secret provider: "wtf"`)

	_, err = fetchSecret(
		ctx,
		ctr,
		"wtf",
		dagger.ContainerWithExecOpts{ExperimentalPrivilegedNesting: true},
	)
	requireErrOut(t, err, `malformed id`)
}

func (SecretProvider) TestEnv(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ctr := c.Container().
		From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

	secretValue := "secret" + identity.NewID()
	out, err := fetchSecret(
		ctx,
		ctr.WithEnvVariable("TOPSECRET", secretValue),
		"env://TOPSECRET",
		dagger.ContainerWithExecOpts{ExperimentalPrivilegedNesting: true},
	)
	require.NoError(t, err)
	require.Equal(t, secretValue, out)

	_, err = fetchSecret(
		ctx,
		ctr,
		"env://TOPSECRET",
		dagger.ContainerWithExecOpts{ExperimentalPrivilegedNesting: true},
	)
	requireErrOut(t, err, `secret env var not found: "TOP..."`)
}

func (SecretProvider) TestFile(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ctr := c.Container().
		From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

	secretValue := "secret" + identity.NewID()
	out, err := fetchSecret(
		ctx,
		ctr.WithNewFile("/tmp/topsecret", secretValue),
		"file:///tmp/topsecret",
		dagger.ContainerWithExecOpts{ExperimentalPrivilegedNesting: true},
	)
	require.NoError(t, err)
	require.Equal(t, secretValue, out)

	_, err = fetchSecret(
		ctx,
		ctr,
		"file:///tmp/topsecret",
		dagger.ContainerWithExecOpts{ExperimentalPrivilegedNesting: true},
	)
	requireErrOut(t, err, "no such file or directory")
}

func (SecretProvider) TestCmd(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ctr := c.Container().
		From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

	secretValue := "secret" + identity.NewID()
	secretValueEncoded := base64.StdEncoding.EncodeToString([]byte(secretValue))
	out, err := fetchSecret(
		ctx,
		ctr,
		`cmd://echo `+secretValueEncoded+` | base64 -d`,
		dagger.ContainerWithExecOpts{ExperimentalPrivilegedNesting: true},
	)
	require.NoError(t, err)
	require.Equal(t, secretValue, out)

	_, err = fetchSecret(
		ctx,
		ctr,
		"cmd://exit 1",
		dagger.ContainerWithExecOpts{ExperimentalPrivilegedNesting: true},
	)
	requireErrOut(t, err, "failed to run secret command")
}

// TODO: implement - but ideally without being dependent on an external service
// func (SecretProvider) TestOnePassword(ctx context.Context, t *testctx.T) {
// }

func (SecretProvider) TestVault(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	vaultImage := c.Container().From("hashicorp/vault:1.18")

	// Configure a Vault server
	vaultServer, err := vaultImage.
		WithEnvVariable("VAULT_DEV_ROOT_TOKEN_ID", "myroot").
		WithEnvVariable("VAULT_DEV_LISTEN_ADDRESS", "0.0.0.0:8200").
		WithEnvVariable("SKIP_SETCAP", "1").
		AsService(dagger.ContainerAsServiceOpts{
			Args: []string{"vault", "server", "-dev"},
		}).Start(ctx)
	require.NoError(t, err)

	// Create a secret with a client
	secretValue := "secret" + identity.NewID()
	_, err = vaultImage.
		WithEnvVariable("VAULT_ADDR", "http://vault:8200").
		WithServiceBinding("vault", vaultServer).
		WithEnvVariable("VAULT_SKIP_VERIFY", "1").
		WithEnvVariable("VAULT_TOKEN", "myroot").
		WithEnvVariable("NOCACHE", time.Now().String()).
		WithExec([]string{"sleep", "5"}). // Wait for server to be ready
		WithExec([]string{"vault", "kv", "put", "/secret/testsecret", "foo=" + secretValue}).
		Sync(ctx)
	require.NoError(t, err)

	// Test Vault provider with token auth
	ctr := c.Container().
		From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithEnvVariable("VAULT_ADDR", "http://vault:8200").
		WithServiceBinding("vault", vaultServer).
		WithEnvVariable("VAULT_SKIP_VERIFY", "1").
		WithEnvVariable("VAULT_TOKEN", "myroot")

	out, err := fetchSecret(
		ctx,
		ctr,
		"vault://testsecret.foo",
		dagger.ContainerWithExecOpts{ExperimentalPrivilegedNesting: true},
	)
	require.NoError(t, err)
	require.Equal(t, secretValue, out)

	_, err = fetchSecret(
		ctx,
		ctr,
		"vault://testsecret.bar",
		dagger.ContainerWithExecOpts{ExperimentalPrivilegedNesting: true},
	)
	requireErrOut(t, err, `secret "bar" not found in path "testsecret"`)

	_, err = fetchSecret(
		ctx,
		ctr,
		"vault://nosecret.baz",
		dagger.ContainerWithExecOpts{ExperimentalPrivilegedNesting: true},
	)
	requireErrOut(t, err, `secret not found`)
}

func (SecretProvider) TestVaultTTL(ctx context.Context, t *testctx.T) {
	var baseContainer = func(c *dagger.Client, vault *dagger.Service) *dagger.Container {
		return c.Container().
			From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithFile("/bin/vault", c.Container().From("hashicorp/vault").File("/bin/vault")).
			WithEnvVariable("VAULT_ADDR", "http://vault:8200").
			WithEnvVariable("VAULT_TOKEN", "vault-root-token").
			WithServiceBinding("vault", vault)
	}

	var verifySecretFromVault = func(ctx context.Context, base *dagger.Container, secretURL string, tcname string) (string, error) {
		return base.
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
// this function updates the value of secret in vault (to simulate expired or changed secret), and sleeps for 5s to allow for ttl (if any) 
// to expire. It then gets its plaintext value again.
// After that it returns both the values as string, which our testcase then verifies.
func (m *Foo) VerifySecret(ctx context.Context, vault *dagger.Service, secret *dagger.Secret, tc string) (string, error) {
	_, err := dag.Container().From("hashicorp/vault").
		WithEnvVariable("VAULT_ADDR", "http://vault:8200").
		WithEnvVariable("VAULT_TOKEN", "vault-root-token").
		WithServiceBinding("vault", vault).
		WithExec([]string{"sh", "-c", fmt.Sprintf("vault kv put secret/%s username=\"admin\" password=\"original-password\"", tc)}).
		Sync(ctx)
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
		WithExec([]string{"sh", "-c", fmt.Sprintf("vault kv put secret/%s username=\"admin\" password=\"updated-password\"", tc)}).Sync(ctx)
	if err != nil {
		return "", err
	}

	// wait for ttl to expire, to simulate a long running process
	time.Sleep(5 * time.Second)

	// now the pipeline needs secret again.
	updated, err := secret.Plaintext(ctx)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("original: %s\nupdated: %s", original, updated), nil
}
`).
			With(daggerCall("-vvv", "verify-secret", fmt.Sprintf("--secret=%s", secretURL), "--vault=tcp://vault:8200", fmt.Sprintf("--tc=%s", tcname))).Stdout(ctx)
	}

	testcases := []struct {
		name                    string
		secret                  string
		expectedUpdatedPassword string
	}{
		{
			name:                    "without-ttl",
			secret:                  "vault://without-ttl.password",
			expectedUpdatedPassword: "original-password",
		},
		{
			name:                    "with-ttl",
			secret:                  "vault://with-ttl.password?ttl=2s",
			expectedUpdatedPassword: "updated-password",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			vault, err := c.Container().
				From("hashicorp/vault").
				WithEnvVariable("VAULT_DEV_ROOT_TOKEN_ID", "vault-root-token").
				WithEnvVariable("VAULT_LOG_LEVEL", "debug").
				WithExposedPort(8200).
				AsService(dagger.ContainerAsServiceOpts{
					UseEntrypoint:                 true,
					ExperimentalPrivilegedNesting: true,
					InsecureRootCapabilities:      true,
				}).Start(ctx)
			require.NoError(t, err)

			base := baseContainer(c, vault).
				WithEnvVariable("CACHE_BUSTER", tc.name)

			output, err := verifySecretFromVault(ctx, base, tc.secret, tc.name)
			require.NoError(t, err)
			require.Equal(t, fmt.Sprintf("original: original-password\nupdated: %s", tc.expectedUpdatedPassword), output)
		})
	}
}

func (SecretProvider) TestGnomeKeyring(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	secretValue := "secret" + identity.NewID()
	secretValueEncoded := base64.StdEncoding.EncodeToString([]byte(secretValue))

	keyringScript := `#!/usr/bin/env sh
set -eux
eval $(echo -n "$" | gnome-keyring-daemon --unlock | sed -e 's/^/export /')
sleep 5 # wait for gnome-keyring-daemon to be ready
"$@"
`

	opts := dagger.ContainerWithExecOpts{
		UseEntrypoint:            true,
		InsecureRootCapabilities: true,
	}

	ctr := c.Container().
		From("alpine").
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		// use a non-nested dev engine, so that the `dagger query` later starts
		// a new session using the environment variable `DBUS_SESSION_BUS_ADDRESS`
		// set by `dbus-run-session` (required so that libsecret can connect to
		// the unlocked gnome-keyring-daemon)
		With(nonNestedDevEngine(c)).
		WithExec([]string{"apk", "add", "libsecret", "gnome-keyring", "dbus-x11", "libcap-setcap"}).
		// HACK: for some reason, looks like this cap needs to be set manually now?
		WithExec([]string{"setcap", "cap_ipc_lock=+ep", "/usr/bin/gnome-keyring-daemon"}).
		WithNewFile("/rest_keyring.sh", keyringScript, dagger.ContainerWithNewFileOpts{
			Permissions: 0755,
		}).
		WithEntrypoint([]string{"dbus-run-session", "--", "/rest_keyring.sh"}).
		WithExec([]string{"sh", "-c", `echo ` + secretValueEncoded + ` | base64 -d | secret-tool store --label=mysecret abc xyz`}, opts).
		// sanity check the secret exists
		WithExec([]string{"secret-tool", "lookup", "abc", "xyz"}, opts)

	result, err := fetchSecret(ctx, ctr, "libsecret://login/mysecret", opts)
	require.NoError(t, err)
	require.Equal(t, secretValue, result)

	result, err = fetchSecret(ctx, ctr, "libsecret://login?abc=xyz", opts)
	require.NoError(t, err)
	require.Equal(t, secretValue, result)
}

func fetchSecret(ctx context.Context, ctr *dagger.Container, url string, opts dagger.ContainerWithExecOpts) (string, error) {
	query := fmt.Sprintf(`{secret(uri: %q) {plaintext}}`, url)
	opts.Stdin = query

	out, err := ctr.WithExec([]string{"dagger", "query"}, opts).Stdout(ctx)
	if err != nil {
		return "", err
	}

	var result struct {
		Secret struct {
			Plaintext string `json:"plaintext"`
		} `json:"secret"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		return "", fmt.Errorf("failed to decode %q: %w", out, err)
	}

	return result.Secret.Plaintext, nil
}
