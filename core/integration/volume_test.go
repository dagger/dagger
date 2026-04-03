package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

type VolumeSuite struct{}

func TestVolume(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(VolumeSuite{})
}

func (VolumeSuite) TestSSHFSVolume(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	sshfs := c.Container().
		From(alpineImage).
		WithExec([]string{"apk", "add", "openssh"})

	hostKeyGen := sshfs.
		WithExec([]string{
			"ssh-keygen", "-t", "rsa", "-b", "4096", "-f", "/root/.ssh/host_key", "-N", "",
		}).
		WithExec([]string{
			"ssh-keygen", "-t", "rsa", "-b", "4096", "-f", "/root/.ssh/id_rsa", "-N", "",
		}).
		WithExec([]string{
			"cp", "/root/.ssh/id_rsa.pub", "/root/.ssh/authorized_keys",
		})

	userPublicKey, err := hostKeyGen.File("/root/.ssh/id_rsa.pub").Contents(ctx)
	require.NoError(t, err)

	userPrivateKey, err := hostKeyGen.File("/root/.ssh/id_rsa").Contents(ctx)
	require.NoError(t, err)

	setupScript := c.Directory().
		WithNewFile("setup.sh", `#!/bin/sh

set -e -u -x

cd /root
mkdir -p repo
cd repo
# Prepare the initial content inside the test.txt
echo test >> test.txt

# Harden and enable key auth
sed -i 's/^#\?PasswordAuthentication.*/PasswordAuthentication no/' /etc/ssh/sshd_config
sed -i 's/^#\?PubkeyAuthentication.*/PubkeyAuthentication yes/' /etc/ssh/sshd_config
grep -q '^PermitRootLogin' /etc/ssh/sshd_config || echo "PermitRootLogin yes" >> /etc/ssh/sshd_config
grep -q '^Subsystem sftp' /etc/ssh/sshd_config || echo "Subsystem sftp /usr/lib/ssh/sftp-server" >> /etc/ssh/sshd_config || true

mkdir -p /var/run/sshd

chmod 700 /root/.ssh
chmod 600 /root/.ssh/authorized_keys

# Copy host key into default search path so we can rely on defaults
cp /root/.ssh/host_key /etc/ssh/ssh_host_rsa_key
cp /root/.ssh/host_key.pub /etc/ssh/ssh_host_rsa_key.pub
chmod 600 /etc/ssh/ssh_host_rsa_key

# Start sshd on port 2222 using its default host key paths
$(which sshd) -D -e -p 2222 &

# Debug: show authorized_keys
echo '--- AUTHORIZED_KEYS START ---'
cat /root/.ssh/authorized_keys || true
echo '--- AUTHORIZED_KEYS END ---'

sleep infinity
`).
		File("setup.sh")

	key, err := ssh.ParseRawPrivateKey([]byte(userPrivateKey))
	require.NoError(t, err)

	sshAgent := agent.NewKeyring()
	err = sshAgent.Add(agent.AddedKey{
		PrivateKey: key,
	})
	require.NoError(t, err)

	tmp := t.TempDir()
	sock := filepath.Join(tmp, "agent.sock")
	l, err := net.Listen("unix", sock)
	require.NoError(t, err)
	defer l.Close()

	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				if !errors.Is(err, net.ErrClosed) {
					t.Logf("accept: %s", err)
					panic(err)
				}
				break
			}

			t.Log("agent serving")

			err = agent.ServeAgent(sshAgent, c)
			if err != nil && !errors.Is(err, io.EOF) {
				t.Logf("serve agent: %s", err)
				panic(err)
			}
		}
	}()

	sshPort := 2222
	sshSvc := hostKeyGen.
		WithMountedFile("/root/start.sh", setupScript).
		WithExposedPort(sshPort).
		WithDefaultArgs([]string{"sh", "/root/start.sh"}).
		AsService()

	// Use the service's internal TCP endpoint (ip:port) directly; engine needs a resolvable target.
	// Resolve the service's container IP by binding it under an alias inside a helper container.
	// Using the raw IP avoids DNS lookup inside the engine for the ephemeral hostname.
	ipLookup := c.Container().
		From(alpineImage).
		WithServiceBinding("ssh", sshSvc).
		WithExec([]string{"sh", "-c", "getent hosts ssh | awk '{print $1; exit}'"})
	ip, ipErr := ipLookup.Stdout(ctx)
	if ipErr != nil || strings.TrimSpace(ip) == "" {
		// Fallback: attempt nslookup (install bind-tools) if getent failed
		ip2, ipErr2 := c.Container().
			From(alpineImage).
			WithExec([]string{"sh", "-c", "apk add --no-cache bind-tools >/dev/null 2>&1; nslookup ssh 2>/dev/null | awk '/^Address: / {print $2; exit}'"}).
			WithServiceBinding("ssh", sshSvc).
			Stdout(ctx)
		if ipErr2 == nil && strings.TrimSpace(ip2) != "" {
			ip = ip2
		} else {
			// Last resort: assume default docker bridge network; may still fail but keeps engine untouched.
			ip = "172.17.0.2" // common first container address; hacky fallback
			t.Logf("service IP resolution failed (getent err=%v nslookup err=%v); falling back to %s", ipErr, ipErr2, ip)
		}
	}
	ip = strings.TrimSpace(ip)

	sshfsEndpoint := fmt.Sprintf("root@%s:%d/root/repo", ip, sshPort)
	t.Logf("sshfs endpoint using resolved service IP %s: %s", ip, sshfsEndpoint)

	privKeySecret := c.SetSecret("sshfs-private-key", userPrivateKey)
	hostKeySecret := c.SetSecret("sshfs-public-key", userPublicKey)

	// readiness: attempt a few ssh connections before proceeding so sshfs mount won't race
	for i := range 10 {
		probe := c.Container().
			From(alpineImage).
			WithServiceBinding("ssh", sshSvc).
			WithFile("/tmp/id_rsa", hostKeyGen.File("/root/.ssh/id_rsa")).
			WithExec([]string{"sh", "-c", fmt.Sprintf("apk add --no-cache openssh-client > /dev/null 2>&1 || true; chmod 600 /tmp/id_rsa; ssh -i /tmp/id_rsa -o IdentitiesOnly=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -p %d root@ssh 'echo ok' 2>&1 || true", sshPort)})
		probeOut, perr := probe.Stdout(ctx)
		if perr == nil && strings.Contains(probeOut, "ok") {
			break
		}
		if i == 9 {
			t.Logf("ssh readiness probe failed after retries; last output: %s", probeOut)
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	sshfsVolume := c.SshfsVolume(sshfsEndpoint, privKeySecret, hostKeySecret)

	// Ensure that the initial content is available to the first container using the volume
	output, err := c.Container().
		From(alpineImage).
		WithVolumeMount("/mnt/repo", sshfsVolume).
		WithExec([]string{"cat", "/mnt/repo/test.txt"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, output, "test")

	// Write some content to the sshfs volume and read it back in a new container
	writeOut, err := c.Container().
		From(alpineImage).
		WithVolumeMount("/mnt/repo", sshfsVolume).
		WithExec([]string{"sh", "-c", "echo 'other' > /mnt/repo/other.txt && cat /mnt/repo/other.txt"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, writeOut, "other")

	// Read the content back in a new container to ensure persistence
	output2, err := c.Container().
		From(alpineImage).
		WithVolumeMount("/mnt/repo", sshfsVolume).
		WithExec([]string{"cat", "/mnt/repo/other.txt"}).
		Stdout(ctx)
	require.NoError(t, err)
	t.Logf("Third container read shows: %q", strings.TrimSpace(output2))
	require.Contains(t, output2, "other")
}
