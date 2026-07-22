package core

// These tests cover opaque filesystem volumes. Unit tests cover validation and
// schema hiding; this suite exercises live engine paths.

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"dagger.io/dagger"
	bkconfig "github.com/dagger/dagger/internal/buildkit/cmd/buildkitd/config"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type VolumeSuite struct{}

func TestVolume(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(VolumeSuite{})
}

func (VolumeSuite) TestEngineVolumeLiveMount(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	parentData := c.CacheVolume("engine-volume-parent-" + identity.NewID())
	nestedData := c.CacheVolume("engine-volume-nested-" + identity.NewID())

	_, err := c.Container().From(alpineImage).
		WithMountedCache("/data", parentData).
		WithExec([]string{"sh", "-ec", "printf root-v1 > /data/root.txt"}).
		Sync(ctx)
	require.NoError(t, err)
	_, err = c.Container().From(alpineImage).
		WithMountedCache("/data", nestedData).
		WithExec([]string{"sh", "-ec", "printf nested-v1 > /data/nested.txt"}).
		Sync(ctx)
	require.NoError(t, err)

	const (
		engineRoot = "/engine-state"
		volumeName = "integration/live"
	)
	operatorPath := engineRoot + "/volumes/v1/integration/live/fs"
	devEngine := devEngineContainer(c,
		engineWithBkConfig(ctx, t, func(_ context.Context, _ *testctx.T, cfg bkconfig.Config) bkconfig.Config {
			cfg.Root = engineRoot
			return cfg
		}),
		func(ctr *dagger.Container) *dagger.Container {
			return ctr.
				WithMountedCache(operatorPath, parentData).
				WithMountedCache(operatorPath+"/nested", nestedData)
		},
	)
	tunneledEngine, err := c.Host().Tunnel(devEngineContainerAsService(devEngine)).Start(ctx)
	require.NoError(t, err)
	engineStopped := false
	t.Cleanup(func() {
		if !engineStopped {
			_, _ = tunneledEngine.Stop(context.WithoutCancel(ctx))
		}
	})

	endpoint, err := tunneledEngine.Endpoint(ctx, dagger.ServiceEndpointOpts{Scheme: "tcp"})
	require.NoError(t, err)
	nestedClient, err := dagger.Connect(ctx,
		dagger.WithRunnerHost(endpoint),
		dagger.WithLogOutput(testutil.NewTWriter(t)),
	)
	require.NoError(t, err)
	clientClosed := false
	t.Cleanup(func() {
		if !clientClosed {
			_ = nestedClient.Close()
		}
	})

	volumeID, err := queryEngineVolumeID(ctx, nestedClient, volumeName)
	require.NoError(t, err)

	out, err := execWithEngineVolume(ctx, nestedClient, volumeID, false, []string{
		"sh", "-ec", "cat /mnt/root.txt; cat /mnt/nested/nested.txt",
	})
	require.NoError(t, err)
	require.Equal(t, "root-v1nested-v1", out)

	_, err = execWithEngineVolume(ctx, nestedClient, volumeID, false, []string{
		"sh", "-ec", "printf root-v2 > /mnt/root.txt; printf nested-v2 > /mnt/nested/nested.txt",
	})
	require.NoError(t, err)
	out, err = execWithEngineVolume(ctx, nestedClient, volumeID, false, []string{
		"sh", "-ec", "cat /mnt/root.txt; cat /mnt/nested/nested.txt",
	})
	require.NoError(t, err)
	require.Equal(t, "root-v2nested-v2", out)

	// On the canonical dev-engine kernel, the startup capability probe enables
	// rbind,rro. Both the volume root and the operator-provided nested mount must
	// reject writes.
	out, err = execWithEngineVolume(ctx, nestedClient, volumeID, true, []string{
		"sh", "-c", "if printf bad > /mnt/root.txt 2>/dev/null; then exit 41; fi; if printf bad > /mnt/nested/nested.txt 2>/dev/null; then exit 42; fi; cat /mnt/root.txt; cat /mnt/nested/nested.txt",
	})
	require.NoError(t, err)
	require.Equal(t, "root-v2nested-v2", out)

	// Cancellation exercises ordinary executor-owned bind teardown. The payload
	// must remain usable by a later mount in the same engine.
	cancelCtx, cancel := context.WithCancel(ctx)
	canceledExec := make(chan error, 1)
	go func() {
		_, err := execWithEngineVolume(cancelCtx, nestedClient, volumeID, false, []string{
			"sh", "-ec", "printf started > /mnt/cancel-started; exec sleep 600",
		})
		canceledExec <- err
	}()

	deadline := time.Now().Add(20 * time.Second)
	for {
		_, err = execWithEngineVolume(ctx, nestedClient, volumeID, false, []string{
			"sh", "-ec", "test -f /mnt/cancel-started",
		})
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for cancellable engine-volume exec to start: %v", err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-canceledExec:
		require.Error(t, err)
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for canceled engine-volume exec to stop")
	}
	out, err = execWithEngineVolume(ctx, nestedClient, volumeID, false, []string{
		"sh", "-ec", "cat /mnt/cancel-started; printf reused > /mnt/after-cancel; cat /mnt/after-cancel",
	})
	require.NoError(t, err)
	require.Equal(t, "startedreused", out)

	require.NoError(t, nestedClient.Close())
	clientClosed = true
	_, err = tunneledEngine.Stop(ctx)
	require.NoError(t, err)
	engineStopped = true

	rootContents, err := c.Container().From(alpineImage).
		WithMountedCache("/data", parentData).
		WithExec([]string{"cat", "/data/root.txt"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "root-v2", rootContents)
	nestedContents, err := c.Container().From(alpineImage).
		WithMountedCache("/data", nestedData).
		WithExec([]string{"cat", "/data/nested.txt"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "nested-v2", nestedContents)
}

func queryEngineVolumeID(ctx context.Context, client *dagger.Client, name string) (string, error) {
	var response struct {
		EngineVolume struct {
			ID string
		}
	}
	err := client.Do(ctx, &dagger.Request{
		Query: `query EngineVolume($name: String!) { engineVolume(name: $name) { id } }`,
		Variables: map[string]any{
			"name": name,
		},
	}, &dagger.Response{Data: &response})
	return response.EngineVolume.ID, err
}

func execWithEngineVolume(ctx context.Context, client *dagger.Client, volumeID string, readonly bool, args []string) (string, error) {
	var response struct {
		Container struct {
			From struct {
				WithEnvVariable struct {
					WithMountedVolume struct {
						WithExec struct {
							Stdout string
						}
					}
				}
			}
		}
	}
	err := client.Do(ctx, &dagger.Request{
		Query: `query EngineVolumeExec($volume: ID!, $readonly: Boolean!, $args: [String!]!, $cacheBuster: String!) {
  container {
    from(address: "` + alpineImage + `") {
      withEnvVariable(name: "ENGINE_VOLUME_TEST_CACHE_BUSTER", value: $cacheBuster) {
        withMountedVolume(path: "/mnt", volume: $volume, readOnly: $readonly) {
          withExec(args: $args) { stdout }
        }
      }
    }
  }
}`,
		Variables: map[string]any{
			"volume":      volumeID,
			"readonly":    readonly,
			"args":        args,
			"cacheBuster": identity.NewID(),
		},
	}, &dagger.Response{Data: &response})
	return response.Container.From.WithEnvVariable.WithMountedVolume.WithExec.Stdout, err
}

func (VolumeSuite) TestSSHFSVolumeMount(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	fixture := newSSHFSVolumeFixture(ctx, t, c, "hello from sshfs\n")

	out, err := c.Container().
		From(alpineImage).
		WithMountedVolume("/mnt", fixture.Volume(c), dagger.ContainerWithMountedVolumeOpts{ReadOnly: true}).
		WithExec([]string{"cat", "/mnt/hello.txt"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, fixture.contents, out)
}

func (VolumeSuite) TestSSHFSVolumeRejectsWrongKnownHosts(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	fixture := newSSHFSVolumeFixture(ctx, t, c, "hello from sshfs\n")

	// Control the fixture first, so the failure below is attributable to
	// host-key verification instead of a broken SSH service.
	out, err := c.Container().
		From(alpineImage).
		WithMountedVolume("/mnt", fixture.Volume(c), dagger.ContainerWithMountedVolumeOpts{ReadOnly: true}).
		WithExec([]string{"cat", "/mnt/hello.txt"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, fixture.contents, out)

	_, err = c.Container().
		From(alpineImage).
		WithMountedVolume("/mnt", fixture.VolumeWithKnownHosts(c, c.SetSecret(
			"sshfs-test-wrong-known-hosts-"+identity.NewID(),
			fixture.wrongKnownHosts,
		))).
		WithExec([]string{"cat", "/mnt/hello.txt"}).
		Stdout(ctx)
	requireErrOut(t, err, "failed to mount /mnt")
}

func (VolumeSuite) TestSSHFSVolumeCachedExecDoesNotRereadRemoteContents(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	fixture := newSSHFSVolumeFixture(ctx, t, c, "v1\n")
	vol := fixture.Volume(c)

	// The read recipe below is intentionally identical before and after the
	// remote write. It should return the cached result, while a cache-busted read
	// proves the remote contents did change.
	readCached := func() string {
		out, err := c.Container().
			From(alpineImage).
			WithMountedVolume("/mnt", vol, dagger.ContainerWithMountedVolumeOpts{ReadOnly: true}).
			WithExec([]string{"cat", "/mnt/hello.txt"}).
			Stdout(ctx)
		require.NoError(t, err)
		return out
	}

	require.Equal(t, "v1\n", readCached())

	_, err := c.Container().
		From(alpineImage).
		WithMountedVolume("/mnt", vol).
		WithExec([]string{"sh", "-c", "printf 'v2\n' > /mnt/hello.txt"}).
		Sync(ctx)
	require.NoError(t, err)

	freshRead, err := c.Container().
		From(alpineImage).
		WithEnvVariable("CACHEBUSTER", identity.NewID()).
		WithMountedVolume("/mnt", vol, dagger.ContainerWithMountedVolumeOpts{ReadOnly: true}).
		WithExec([]string{"cat", "/mnt/hello.txt"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "v2\n", freshRead)

	require.Equal(t, "v1\n", readCached())
}

func (VolumeSuite) TestModuleAcceptsAndMountsVolume(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	fixture := newSSHFSVolumeFixture(ctx, t, c, "hello from a module\n")
	volID, err := fixture.Volume(c).ID(ctx)
	require.NoError(t, err)

	modDir := t.TempDir()
	copyTestdataFixture(ctx, t, modDir, "modules", "go", "call-volume")
	err = c.ModuleSource(modDir).AsModule().Serve(ctx)
	require.NoError(t, err)

	res, err := testutil.QueryWithClient[struct {
		Test struct {
			Read string
		}
	}](c, t, `query($volume: ID!) { test { read(volume: $volume) } }`, &testutil.QueryOptions{
		Variables: map[string]any{
			"volume": volID,
		},
	})
	require.NoError(t, err)
	require.Equal(t, fixture.contents, res.Test.Read)
}

func (VolumeSuite) TestModuleCannotConstructSSHFSVolume(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	_, err := moduleFixture(t, c, "go/sshfs-volume-constructor-denied").
		With(daggerCallAt(".", "fn")).
		Sync(ctx)
	requireErrOut(t, err, "SshfsVolume")
	requireErrOut(t, err, "undefined")
}

type sshfsVolumeFixture struct {
	endpoint   string
	privateKey *dagger.Secret
	knownHosts *dagger.Secret
	service    *dagger.Service
	contents   string

	wrongKnownHosts string
}

func (f sshfsVolumeFixture) Volume(c *dagger.Client) *dagger.Volume {
	return f.VolumeWithKnownHosts(c, f.knownHosts)
}

func (f sshfsVolumeFixture) VolumeWithKnownHosts(c *dagger.Client, knownHosts *dagger.Secret) *dagger.Volume {
	return c.SshfsVolume(f.endpoint, f.privateKey, dagger.SshfsVolumeOpts{
		KnownHosts:              knownHosts,
		ExperimentalServiceHost: f.service,
	})
}

func newSSHFSVolumeFixture(ctx context.Context, t *testctx.T, c *dagger.Client, contents string) sshfsVolumeFixture {
	t.Helper()

	const (
		sshPort     = 2222
		logicalHost = "example.com"
	)

	sshBase := c.Container().
		From(alpineImage).
		WithExec([]string{"apk", "add", "openssh"})

	keygen := sshBase.WithExec([]string{"sh", "-ec", `
mkdir -p /root/.ssh
ssh-keygen -t ed25519 -f /root/.ssh/host_key -N ""
ssh-keygen -t ed25519 -f /root/.ssh/id_ed25519 -N ""
`})

	hostPubKey, err := keygen.File("/root/.ssh/host_key.pub").Contents(ctx)
	require.NoError(t, err)

	userPrivateKey, err := keygen.File("/root/.ssh/id_ed25519").Contents(ctx)
	require.NoError(t, err)

	userPubKey, err := keygen.File("/root/.ssh/id_ed25519.pub").Contents(ctx)
	require.NoError(t, err)

	setupScript := c.Directory().
		WithNewFile("start.sh", `#!/bin/sh
set -eu

mkdir -p /run/sshd /root/.ssh
cp /root/.ssh/id_ed25519.pub /root/.ssh/authorized_keys
chmod 700 /root/.ssh
chmod 600 /root/.ssh/authorized_keys
chmod 600 /root/.ssh/host_key
if [ ! -e /data/hello.txt ]; then
	cp /seed/hello.txt /data/hello.txt
fi
chmod 644 /data/hello.txt

cat > /etc/ssh/sshd_config <<'EOF'
Port 2222
ListenAddress 0.0.0.0
HostKey /root/.ssh/host_key
PasswordAuthentication no
PubkeyAuthentication yes
PermitRootLogin prohibit-password
AuthorizedKeysFile .ssh/authorized_keys
Subsystem sftp internal-sftp
EOF

exec "$(which sshd)" -D -e -f /etc/ssh/sshd_config
`, dagger.DirectoryWithNewFileOpts{Permissions: 0o755}).
		File("start.sh")

	service := keygen.
		WithNewFile("/seed/hello.txt", contents).
		// Keep remote writes across service restarts; use a unique key so
		// parallel tests and prior runs cannot inherit each other's contents.
		WithMountedCache("/data", c.CacheVolume("sshfs-test-data-"+identity.NewID())).
		WithMountedFile("/root/start.sh", setupScript).
		WithExposedPort(sshPort).
		WithDefaultArgs([]string{"sh", "/root/start.sh"}).
		AsService()

	return sshfsVolumeFixture{
		endpoint:   fmt.Sprintf("sshfs://root@%s:%d/data", logicalHost, sshPort),
		privateKey: c.SetSecret("sshfs-test-private-key-"+identity.NewID(), userPrivateKey),
		knownHosts: c.SetSecret(
			"sshfs-test-known-hosts-"+identity.NewID(),
			fmt.Sprintf("[%s]:%d %s", logicalHost, sshPort, strings.TrimSpace(hostPubKey)),
		),
		service:  service,
		contents: contents,
		wrongKnownHosts: fmt.Sprintf(
			"[%s]:%d %s",
			logicalHost,
			sshPort,
			strings.TrimSpace(userPubKey),
		),
	}
}
