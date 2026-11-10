package core

// These tests cover the host policy that a client can run with
// (_EXPERIMENTAL_DAGGER_HOST_POLICY). The client serves only the host access
// that the policy allows, and the first refused request stops the session.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"dagger.io/dagger/engineconn"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type HostPolicySuite struct{}

func TestHostPolicy(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(HostPolicySuite{})
}

const (
	hostPolicyEnv     = "_EXPERIMENTAL_DAGGER_HOST_POLICY"
	hostPolicyRefusal = "host access denied by session policy (test)"
)

// connectWithHostPolicy starts a new `dagger session` with the policy, as an
// SDK does, instead of joining the test's session. It returns the session's
// log output too.
func connectWithHostPolicy(ctx context.Context, t *testctx.T, workdir, policy string, env ...string) (*dagger.Client, *safeBuffer) {
	t.Helper()
	logs := &safeBuffer{}
	cfg := engineconn.Config{
		Workdir:   workdir,
		LogOutput: logs,
		UnsetEnv:  []string{"DAGGER_SESSION_PORT", "DAGGER_SESSION_TOKEN"},
		ExtraEnv:  append([]string{hostPolicyEnv + "=" + policy}, env...),
	}
	conn, found, err := engineconn.FromLocalCLI(ctx, &cfg)
	require.NoError(t, err)
	require.True(t, found, "set _EXPERIMENTAL_DAGGER_CLI_BIN to the from-source CLI")
	c, err := dagger.Connect(ctx, dagger.WithConn(conn))
	require.NoError(t, err)
	return c, logs
}

// hostPolicyCLI runs the CLI in its own session with the policy.
func hostPolicyCLI(ctx context.Context, t *testctx.T, workdir, policy string, env []string, args ...string) *exec.Cmd {
	t.Helper()
	cmd := hostDaggerCommandRaw(ctx, t, workdir, args...)
	cmd.Env = slices.DeleteFunc(cmd.Env, func(kv string) bool {
		return strings.HasPrefix(kv, "DAGGER_SESSION_PORT=") || strings.HasPrefix(kv, "DAGGER_SESSION_TOKEN=")
	})
	cmd.Env = append(cmd.Env, hostPolicyEnv+"="+policy)
	cmd.Env = append(cmd.Env, env...)
	return cmd
}

func (HostPolicySuite) TestCoreAPIWithoutHostAccess(ctx context.Context, t *testctx.T) {
	c, _ := connectWithHostPolicy(ctx, t, t.TempDir(), `{"version": 1, "reason": "test"}`)
	q := core.NewQuery(c)

	out, err := q.Container().From(alpineImage).WithExec([]string{"echo", "hello"}).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello\n", out)
	require.NoError(t, c.Close())
}

func (HostPolicySuite) TestSecretProviders(ctx context.Context, t *testctx.T) {
	marker := filepath.Join(t.TempDir(), "marker")
	c, logs := connectWithHostPolicy(ctx, t, t.TempDir(),
		`{"version": 1, "reason": "test", "secrets": {"providers": ["env"]}}`,
		"HOST_POLICY_TOKEN=hello")
	q := core.NewQuery(c)

	plaintext, err := q.Secret("env://HOST_POLICY_TOKEN").Plaintext(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello", plaintext)

	_, err = q.Secret("cmd://touch " + marker).Plaintext(ctx)
	require.ErrorContains(t, err, hostPolicyRefusal+`: secret provider "cmd" is not allowed (allowed: env)`)
	require.NoFileExists(t, marker)

	// The refusal stopped the session: `dagger session` exits with an error
	// and logs the refusal.
	_, err = q.Container().From(alpineImage).WithExec([]string{"true"}).Sync(ctx)
	require.Error(t, err)
	require.Error(t, c.Close())
	require.Contains(t, logs.String(), hostPolicyRefusal)
}

func (HostPolicySuite) TestFileReads(ctx context.Context, t *testctx.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "inside.txt"), []byte("inside"), 0o600))
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o600))
	policy := fmt.Sprintf(`{"version": 1, "reason": "test", "files": {"read": [%q]}}`, root)

	t.Run("under a read path", func(ctx context.Context, t *testctx.T) {
		c, _ := connectWithHostPolicy(ctx, t, root, policy)
		q := core.NewQuery(c)
		contents, err := q.Host().File(filepath.Join(root, "inside.txt")).Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "inside", contents)
		require.NoError(t, c.Close())
	})

	t.Run("outside the read paths", func(ctx context.Context, t *testctx.T) {
		// Outside the read paths, the host looks empty.
		c, _ := connectWithHostPolicy(ctx, t, root, policy)
		q := core.NewQuery(c)
		_, err := q.Host().File(filepath.Join(outside, "secret.txt")).Contents(ctx)
		require.ErrorContains(t, err, hostPolicyRefusal+": "+outside+" is outside the read paths")
		require.NoError(t, c.Close())
	})
}

func (HostPolicySuite) TestHostSockets(ctx context.Context, t *testctx.T) {
	c, _ := connectWithHostPolicy(ctx, t, t.TempDir(), `{"version": 1, "reason": "test"}`)
	q := core.NewQuery(c)

	_, err := q.Container().From(alpineImage).
		WithUnixSocket("/tmp/host.sock", q.Host().UnixSocket("/var/run/docker.sock")).
		WithExec([]string{"sh", "-c", "echo hello | nc local:/tmp/host.sock"}).
		Sync(ctx)
	require.ErrorContains(t, err, hostPolicyRefusal+": host socket")
	require.Error(t, c.Close())
}

func (HostPolicySuite) TestHostTunnels(ctx context.Context, t *testctx.T) {
	c, _ := connectWithHostPolicy(ctx, t, t.TempDir(), `{"version": 1, "reason": "test"}`)
	q := core.NewQuery(c)

	svc, _ := httpService(ctx, t, c, "hello")
	_, err := q.Host().Tunnel(svc).Start(ctx)
	require.ErrorContains(t, err, hostPolicyRefusal+": listening on host ports is not allowed")
	require.Error(t, c.Close())
}

const hostPolicyConsumerModule = `name = "consumer"
engineVersion = "v1.0.0-beta.20"

[runtime]
source = "dang"
`

const hostPolicyConsumerSource = `type Consumer {
  let token: Secret

  new(token: Secret = null) {
    self.token = token
    self
  }

  pub verify: Void @check {
    null
  }
}
`

// hostPolicyWorkspace writes a workspace with the consumer module, whose token
// setting is the given secret address.
func hostPolicyWorkspace(ctx context.Context, t *testctx.T, token string) string {
	t.Helper()
	dir := t.TempDir()
	initGitRepo(ctx, t, dir)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "consumer"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "consumer", "dagger-module.toml"), []byte(hostPolicyConsumerModule), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "consumer", "main.dang"), []byte(hostPolicyConsumerSource), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "dagger.toml"), []byte(fmt.Sprintf(`[modules.consumer]
source = "consumer"

[modules.consumer.settings]
token = %q
`, token)), 0o600))
	return dir
}

func (HostPolicySuite) TestWorkspaceSettings(ctx context.Context, t *testctx.T) {
	t.Run("allowed provider", func(ctx context.Context, t *testctx.T) {
		dir := hostPolicyWorkspace(ctx, t, "env://HOST_POLICY_TOKEN")
		policy := fmt.Sprintf(`{"version": 1, "reason": "test", "secrets": {"providers": ["env"]}, "files": {"read": [%q]}}`, dir)
		out, err := hostPolicyCLI(ctx, t, dir, policy, []string{"HOST_POLICY_TOKEN=hello"}, "check", "--module=consumer").CombinedOutput()
		require.NoError(t, err, string(out))
	})

	t.Run("refused provider", func(ctx context.Context, t *testctx.T) {
		marker := filepath.Join(t.TempDir(), "marker")
		dir := hostPolicyWorkspace(ctx, t, "cmd://touch "+marker)
		policy := fmt.Sprintf(`{"version": 1, "reason": "test", "secrets": {"providers": ["env"]}, "files": {"read": [%q]}}`, dir)
		out, err := hostPolicyCLI(ctx, t, dir, policy, nil, "check", "--module=consumer").CombinedOutput()
		require.Error(t, err, string(out))
		// The check does not use the token, so it passes. The refusal still
		// fails the command.
		require.Contains(t, string(out), hostPolicyRefusal+": secret provider")
		require.NoFileExists(t, marker)
	})
}

func (HostPolicySuite) TestPolicyFailsClosed(ctx context.Context, t *testctx.T) {
	t.Run("invalid policy", func(ctx context.Context, t *testctx.T) {
		cmd := hostPolicyCLI(ctx, t, t.TempDir(), `{"version": 2}`, nil, "query")
		cmd.Stdin = strings.NewReader(`{version}`)
		out, err := cmd.CombinedOutput()
		require.Error(t, err, string(out))
		require.Contains(t, string(out), hostPolicyEnv+": host policy version 2 is not supported")
	})

	t.Run("nested session", func(ctx context.Context, t *testctx.T) {
		// A nested client uses the host services of the session that it joins.
		cmd := hostPolicyCLI(ctx, t, t.TempDir(), `{"version": 1}`, []string{"DAGGER_SESSION_PORT=1"}, "query")
		cmd.Stdin = strings.NewReader(`{version}`)
		out, err := cmd.CombinedOutput()
		require.Error(t, err, string(out))
		require.Contains(t, string(out), "a host policy cannot apply to a nested session")
	})
}
