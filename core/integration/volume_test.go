package core

// These tests cover opaque filesystem volumes. Unit tests cover validation and
// schema hiding; this suite exercises live engine paths.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	engineState := c.CacheVolume("engine-volume-state-" + identity.NewID())
	groupData := c.CacheVolume("engine-volume-group-" + identity.NewID())
	nestedData := c.CacheVolume("engine-volume-nested-" + identity.NewID())

	_, err := c.Container().From(alpineImage).
		WithMountedCache("/state", engineState).
		WithExec([]string{"sh", "-ec", `
mkdir -p /state/volumes/v1/outside/fs
printf clamped-name > /state/volumes/v1/outside/fs/hello.txt
`}).
		Sync(ctx)
	require.NoError(t, err)
	_, err = c.Container().From(alpineImage).
		WithMountedCache("/data", groupData).
		WithExec([]string{"sh", "-ec", `
mkdir -p /data/models/fs/sub/dir /data/models/fs/outside-subdir /data/cache/fs
printf root-v1 > /data/models/fs/root.txt
printf operator-root > /data/models/fs/hello.txt
printf operator-subdir > /data/models/fs/sub/dir/hello.txt
printf clamped-subdir > /data/models/fs/outside-subdir/hello.txt
printf grouped-cache > /data/cache/fs/hello.txt
printf payload > /data/models/fs/file
printf operator-file > /data/wrong
ln -s sub/dir /data/models/fs/internal-link
ln -s /outside-subdir /data/models/fs/escape
ln -s /outside /data/link
`}).
		Sync(ctx)
	require.NoError(t, err)
	_, err = c.Container().From(alpineImage).
		WithMountedCache("/data", nestedData).
		WithExec([]string{"sh", "-ec", "printf nested-v1 > /data/nested.txt"}).
		Sync(ctx)
	require.NoError(t, err)

	const (
		engineRoot    = "/engine-state"
		groupRoot     = engineRoot + "/volumes/v1/team-a"
		modelsRoot    = groupRoot + "/models/fs"
		modelsVolName = "team-a/models"
	)
	devEngine := devEngineContainer(c,
		engineWithBkConfig(ctx, t, func(_ context.Context, _ *testctx.T, cfg bkconfig.Config) bkconfig.Config {
			cfg.Root = engineRoot
			return cfg
		}),
		func(ctr *dagger.Container) *dagger.Container {
			return ctr.
				WithMountedCache(engineRoot, engineState).
				WithMountedCache(groupRoot, groupData).
				WithMountedCache(modelsRoot+"/nested", nestedData).
				WithNewFile("/outside/fs/hello.txt", "uncontained-name").
				WithNewFile("/outside-subdir/hello.txt", "uncontained-subdir")
		},
	)
	engineService := devEngineContainerAsService(devEngine)
	tunneledEngine, err := c.Host().Tunnel(engineService).Start(ctx)
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

	secondClient, err := dagger.Connect(ctx,
		dagger.WithRunnerHost(endpoint),
		dagger.WithLogOutput(testutil.NewTWriter(t)),
	)
	require.NoError(t, err)
	secondClientClosed := false
	t.Cleanup(func() {
		if !secondClientClosed {
			_ = secondClient.Close()
		}
	})

	volumeID, err := nestedClient.EngineVolume(modelsVolName).ID(ctx)
	require.NoError(t, err)
	secondVolumeID, err := secondClient.EngineVolume(modelsVolName).ID(ctx)
	require.NoError(t, err)
	require.Equal(t, volumeID, secondVolumeID)

	out, err := execWithEngineVolume(ctx, nestedClient, volumeID, false, []string{
		"sh", "-ec", "cat /mnt/root.txt; cat /mnt/nested/nested.txt; cat /mnt/internal-link/hello.txt",
	})
	require.NoError(t, err)
	require.Equal(t, "root-v1nested-v1operator-subdir", out)

	groupedVolumeID, err := nestedClient.EngineVolume("team-a/cache").ID(ctx)
	require.NoError(t, err)
	out, err = execWithEngineVolume(ctx, nestedClient, groupedVolumeID, false, []string{"cat", "/mnt/hello.txt"})
	require.NoError(t, err)
	require.Equal(t, "grouped-cache", out)

	subdirVolumeID, err := nestedClient.EngineVolume(modelsVolName, dagger.EngineVolumeOpts{Subdir: "sub/dir"}).ID(ctx)
	require.NoError(t, err)
	out, err = execWithEngineVolume(ctx, nestedClient, subdirVolumeID, false, []string{"cat", "/mnt/hello.txt"})
	require.NoError(t, err)
	require.Equal(t, "operator-subdir", out)

	// A CLI running outside the engine has a conflicting same-named local path.
	// The Volume argument address must resolve against the remote engine and be
	// passed opaquely into the module.
	moduleDir := t.TempDir()
	copyTestdataFixture(ctx, t, moduleDir, "modules", "go", "call-volume")
	callerLocalPath := filepath.Join(moduleDir, "volumes", "v1", "team-a", "models", "fs", "sub", "dir")
	require.NoError(t, os.MkdirAll(callerLocalPath, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(callerLocalPath, "hello.txt"), []byte("caller-local"), 0o644))
	runCLI := func(address string) (string, error) {
		cmd := hostDaggerCommandRaw(ctx, t, moduleDir, "call", "-m", ".", "read", "--volume", address)
		cmd.Env = append(cmd.Env, "_EXPERIMENTAL_DAGGER_RUNNER_HOST="+endpoint)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		if err != nil {
			return stdout.String() + stderr.String(), err
		}
		return stdout.String(), nil
	}
	out, err = runCLI("engine-volume://team-a/models?subdir=sub%2Fdir")
	require.NoError(t, err)
	require.Equal(t, "operator-subdir", strings.TrimSpace(out))

	out, err = runCLI("engine-volume://team-a/models?subdir=sub&subdir=dir")
	require.Error(t, err)
	require.Contains(t, out, "must be specified once")
	out, err = runCLI("engine-volume://team-a/models?unknown=value")
	require.Error(t, err)
	require.Contains(t, out, "unsupported volume address query parameter")

	_, err = execWithEngineVolume(ctx, nestedClient, volumeID, false, []string{
		"sh", "-ec", "printf root-v2 > /mnt/root.txt; printf nested-v2 > /mnt/nested/nested.txt",
	})
	require.NoError(t, err)
	out, err = execWithEngineVolume(ctx, nestedClient, volumeID, false, []string{
		"sh", "-ec", "cat /mnt/root.txt; cat /mnt/nested/nested.txt",
	})
	require.NoError(t, err)
	require.Equal(t, "root-v2nested-v2", out)

	_, err = execWithEngineVolume(ctx, nestedClient, volumeID, false, []string{
		"sh", "-ec", "printf shared-v1 > /mnt/shared.txt",
	})
	require.NoError(t, err)
	out, err = execWithEngineVolume(ctx, secondClient, secondVolumeID, false, []string{"cat", "/mnt/shared.txt"})
	require.NoError(t, err)
	require.Equal(t, "shared-v1", out)
	_, err = execWithEngineVolume(ctx, secondClient, secondVolumeID, false, []string{
		"sh", "-ec", "printf shared-v2 > /mnt/shared.txt",
	})
	require.NoError(t, err)
	out, err = execWithEngineVolume(ctx, nestedClient, volumeID, false, []string{"cat", "/mnt/shared.txt"})
	require.NoError(t, err)
	require.Equal(t, "shared-v2", out)

	_, err = execWithEngineVolume(ctx, nestedClient, volumeID, false, []string{
		"sh", "-ec", "printf cache-v1 > /mnt/cache.txt",
	})
	require.NoError(t, err)
	readArgs := []string{"cat", "/mnt/cache.txt"}
	out, err = execWithEngineVolumeCached(ctx, nestedClient, volumeID, false, readArgs, "stable-read")
	require.NoError(t, err)
	require.Equal(t, "cache-v1", out)
	_, err = execWithEngineVolume(ctx, secondClient, secondVolumeID, false, []string{
		"sh", "-ec", "printf cache-v2 > /mnt/cache.txt",
	})
	require.NoError(t, err)
	out, err = execWithEngineVolume(ctx, nestedClient, volumeID, false, readArgs)
	require.NoError(t, err)
	require.Equal(t, "cache-v2", out)
	out, err = execWithEngineVolumeCached(ctx, nestedClient, volumeID, false, readArgs, "stable-read")
	require.NoError(t, err)
	require.Equal(t, "cache-v1", out)

	missingSubdirID, err := queryEngineVolumeID(ctx, nestedClient, modelsVolName, "missing")
	require.NoError(t, err)
	_, err = execWithEngineVolume(ctx, nestedClient, missingSubdirID, false, []string{"true"})
	require.ErrorContains(t, err, "subdir \"missing\" does not exist")
	out, err = execWithEngineVolume(ctx, nestedClient, volumeID, false, []string{"sh", "-ec", "test ! -e /mnt/missing; printf absent"})
	require.NoError(t, err)
	require.Equal(t, "absent", out)

	fileSubdirID, err := queryEngineVolumeID(ctx, nestedClient, modelsVolName, "file")
	require.NoError(t, err)
	_, err = execWithEngineVolume(ctx, nestedClient, fileSubdirID, false, []string{"true"})
	require.ErrorContains(t, err, "subdir \"file\" is not a directory")

	_, err = queryEngineVolumeID(ctx, nestedClient, "../escape")
	require.Error(t, err)
	wrongTypeID, err := queryEngineVolumeID(ctx, nestedClient, "team-a/wrong")
	require.NoError(t, err)
	_, err = execWithEngineVolume(ctx, nestedClient, wrongTypeID, false, []string{"true"})
	require.ErrorContains(t, err, "not a directory")

	clampedSubdirID, err := queryEngineVolumeID(ctx, nestedClient, modelsVolName, "escape")
	require.NoError(t, err)
	out, err = execWithEngineVolume(ctx, nestedClient, clampedSubdirID, false, []string{"cat", "/mnt/hello.txt"})
	require.NoError(t, err)
	require.Equal(t, "clamped-subdir", out)
	clampedNameID, err := queryEngineVolumeID(ctx, nestedClient, "team-a/link")
	require.NoError(t, err)
	out, err = execWithEngineVolume(ctx, nestedClient, clampedNameID, false, []string{"cat", "/mnt/hello.txt"})
	require.NoError(t, err)
	require.Equal(t, "clamped-name", out)

	createdID, err := queryEngineVolumeID(ctx, nestedClient, "created/new")
	require.NoError(t, err)
	out, err = execWithEngineVolume(ctx, nestedClient, createdID, false, []string{
		"sh", "-ec", "printf '%s:' \"$(stat -c %a /mnt)\"; printf created > /mnt/hello.txt; cat /mnt/hello.txt",
	})
	require.NoError(t, err)
	require.Equal(t, "755:created", out)

	concurrentID, err := queryEngineVolumeID(ctx, nestedClient, "created/concurrent")
	require.NoError(t, err)
	const concurrentUsers = 8
	errCh := make(chan error, concurrentUsers)
	var wg sync.WaitGroup
	for i := range concurrentUsers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := execWithEngineVolume(ctx, nestedClient, concurrentID, false, []string{
				"sh", "-ec", fmt.Sprintf("printf worker > /mnt/worker-%d", i),
			})
			errCh <- err
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		require.NoError(t, err)
	}
	out, err = execWithEngineVolume(ctx, nestedClient, concurrentID, false, []string{
		"sh", "-ec", "find /mnt -type f | wc -l",
	})
	require.NoError(t, err)
	require.Equal(t, fmt.Sprintf("%d\n", concurrentUsers), out)

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

	// Closing a separate client with an active exec exercises session-disconnect
	// teardown. The source must be immediately reusable by another session.
	disconnectClient, err := dagger.Connect(ctx,
		dagger.WithRunnerHost(endpoint),
		dagger.WithLogOutput(testutil.NewTWriter(t)),
	)
	require.NoError(t, err)
	disconnectClosed := false
	t.Cleanup(func() {
		if !disconnectClosed {
			_ = disconnectClient.Close()
		}
	})
	disconnectVolumeID, err := queryEngineVolumeID(ctx, disconnectClient, modelsVolName)
	require.NoError(t, err)
	disconnectedExec := make(chan error, 1)
	go func() {
		_, err := execWithEngineVolume(context.WithoutCancel(ctx), disconnectClient, disconnectVolumeID, false, []string{
			"sh", "-ec", "printf started > /mnt/disconnect-started; exec sleep 600",
		})
		disconnectedExec <- err
	}()
	waitForEngineVolumeFile(ctx, t, nestedClient, volumeID, "/mnt/disconnect-started")
	require.NoError(t, disconnectClient.Close())
	disconnectClosed = true
	select {
	case err := <-disconnectedExec:
		require.Error(t, err)
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for disconnected engine-volume exec to stop")
	}
	out, err = execWithEngineVolume(ctx, nestedClient, volumeID, false, []string{
		"sh", "-ec", "cat /mnt/disconnect-started; printf reused > /mnt/after-disconnect; cat /mnt/after-disconnect",
	})
	require.NoError(t, err)
	require.Equal(t, "startedreused", out)

	require.NoError(t, secondClient.Close())
	secondClientClosed = true
	require.NoError(t, nestedClient.Close())
	clientClosed = true
	_, err = tunneledEngine.Stop(ctx)
	require.NoError(t, err)
	engineStopped = true

	rootContents, err := c.Container().From(alpineImage).
		WithMountedCache("/data", groupData).
		WithExec([]string{"sh", "-ec", "cat /data/models/fs/root.txt; cat /data/models/fs/shared.txt"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "root-v2shared-v2", rootContents)
	nestedContents, err := c.Container().From(alpineImage).
		WithMountedCache("/data", nestedData).
		WithExec([]string{"cat", "/data/nested.txt"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "nested-v2", nestedContents)
	createdContents, err := c.Container().From(alpineImage).
		WithMountedCache("/state", engineState).
		WithExec([]string{"sh", "-ec", "printf '%s:' \"$(stat -c %a /state/volumes/v1/created/new/fs)\"; cat /state/volumes/v1/created/new/fs/hello.txt"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "755:created", createdContents)
}

func queryEngineVolumeID(ctx context.Context, client *dagger.Client, name string, subdir ...string) (dagger.ID, error) {
	var response struct {
		EngineVolume struct {
			ID dagger.ID
		}
	}
	var subdirValue any
	if len(subdir) > 0 {
		subdirValue = subdir[0]
	}
	err := client.Do(ctx, &dagger.Request{
		Query: `query EngineVolume($name: String!, $subdir: String) { engineVolume(name: $name, subdir: $subdir) { id } }`,
		Variables: map[string]any{
			"name":   name,
			"subdir": subdirValue,
		},
	}, &dagger.Response{Data: &response})
	return response.EngineVolume.ID, err
}

func execWithEngineVolume(ctx context.Context, client *dagger.Client, volumeID dagger.ID, readonly bool, args []string) (string, error) {
	return execWithEngineVolumeCached(ctx, client, volumeID, readonly, args, identity.NewID())
}

func execWithEngineVolumeCached(ctx context.Context, client *dagger.Client, volumeID dagger.ID, readonly bool, args []string, cacheBuster string) (string, error) {
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
			"cacheBuster": cacheBuster,
		},
	}, &dagger.Response{Data: &response})
	return response.Container.From.WithEnvVariable.WithMountedVolume.WithExec.Stdout, err
}

func waitForEngineVolumeFile(ctx context.Context, t *testctx.T, client *dagger.Client, volumeID dagger.ID, path string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		_, err := execWithEngineVolume(ctx, client, volumeID, false, []string{"test", "-f", path})
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for engine-volume file %q: %v", path, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
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

func (VolumeSuite) TestModuleCannotConstructEngineVolume(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	_, err := moduleFixture(t, c, "go/engine-volume-constructor-denied").
		With(daggerCallAt(".", "fn")).
		Sync(ctx)
	requireErrOut(t, err, "EngineVolume")
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
