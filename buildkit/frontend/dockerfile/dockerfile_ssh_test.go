package dockerfile

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/containerd/continuity/fs/fstest"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/frontend/dockerui"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/sshforward/sshprovider"
	"github.com/moby/buildkit/util/testutil/integration"
	"github.com/stretchr/testify/require"
	"github.com/tonistiigi/fsutil"
)

var sshTests = integration.TestFuncs(
	testSSHSocketParams,
	testSSHFileDescriptorsClosed,
)

func init() {
	allTests = append(allTests, sshTests...)
}

func testSSHSocketParams(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM busybox
RUN --mount=type=ssh,mode=741,uid=100,gid=102 [ "$(stat -c "%u %g %f" $SSH_AUTH_SOCK)" = "100 102 c1e1" ]
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	k, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	dt := pem.EncodeToMemory(
		&pem.Block{
			Type:  "RSA PRIVATE KEY",
			Bytes: x509.MarshalPKCS1PrivateKey(k),
		},
	)

	tmpDir := t.TempDir()

	err = os.WriteFile(filepath.Join(tmpDir, "key"), dt, 0600)
	require.NoError(t, err)

	ssh, err := sshprovider.NewSSHAgentProvider([]sshprovider.AgentConfig{{
		Paths: []string{filepath.Join(tmpDir, "key")},
	}})
	require.NoError(t, err)

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
		Session: []session.Attachable{ssh},
	}, nil)
	require.NoError(t, err)
}

func testSSHFileDescriptorsClosed(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM alpine
RUN --mount=type=ssh apk update \
 && apk add openssh-client-default \
 && mkdir -p -m 0600 ~/.ssh \
 && ssh-keyscan github.com >> ~/.ssh/known_hosts \
 && for i in $(seq 1 3); do \
        ssh -T git@github.com; \
    done; \
    exit 0;
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	// not using t.TempDir() here because the path ends up longer than the unix socket max length
	tmpDir, err := os.MkdirTemp("", "buildkit-ssh-test-")
	require.NoError(t, err)
	t.Cleanup(func() {
		os.RemoveAll(tmpDir)
	})
	sockPath := filepath.Join(tmpDir, "ssh-agent.sock")

	sshAgentCmd := exec.CommandContext(sb.Context(), "ssh-agent", "-s", "-d", "-a", sockPath)
	sshAgentOutputBuf := &bytes.Buffer{}
	sshAgentCmd.Stderr = sshAgentOutputBuf
	require.NoError(t, sshAgentCmd.Start())
	var found bool
	for i := 0; i < 100; i++ {
		_, err := os.Stat(sockPath)
		if err == nil {
			found = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !found {
		sshAgentOutput := sshAgentOutputBuf.String()
		t.Fatalf("ssh-agent failed to start: %s", sshAgentOutput)
	}

	ssh, err := sshprovider.NewSSHAgentProvider([]sshprovider.AgentConfig{{
		Paths: []string{sockPath},
	}})
	require.NoError(t, err)

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
		Session: []session.Attachable{ssh},
	}, nil)
	require.NoError(t, err)

	sshAgentOutput := sshAgentOutputBuf.String()
	require.Contains(t, sshAgentOutput, "process_message: socket 1")
	require.NotContains(t, sshAgentOutput, "process_message: socket 2")
	require.NotContains(t, sshAgentOutput, "process_message: socket 3")
}
