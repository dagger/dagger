package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dagger/dagger/dagql"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

func TestVolumePersistedObjectRoundTrip(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	ctx = dagql.ContextWithCache(ctx, cache)

	query := &Query{Server: &cacheVolumeTestQueryServer{mockServer: &mockServer{}}}
	srv := newCoreDagqlServerForTest(t, query)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Secret]{}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Service]{}))

	privateKey := volumeTestCachedObjectResult(t, ctx, cache, srv, "volume-session", "private-key", &Secret{
		Handle:  "private-key-handle",
		NameVal: "private-key",
	})
	knownHosts := volumeTestCachedObjectResult(t, ctx, cache, srv, "volume-session", "known-hosts", &Secret{
		Handle:  "known-hosts-handle",
		NameVal: "known-hosts",
	})
	serviceHost := volumeTestCachedObjectResult(t, ctx, cache, srv, "volume-session", "service-host", &Service{
		CustomHostname: "ssh.example.test",
	})
	vol := &Volume{
		Backend: VolumeBackendKindSSHFS,
		SSHFS: &SSHFSVolumeConfig{
			Endpoint:                 "sshfs://git@example.com:2222/srv/repo",
			PrivateKey:               privateKey,
			KnownHosts:               knownHosts,
			InsecureSkipHostKeyCheck: false,
			HostKeyAlias:             "[example.com]:2222",
			ServiceHost:              serviceHost,
		},
	}

	encoded, err := vol.EncodePersistedObject(ctx, cache)
	require.NoError(t, err)
	var raw persistedVolumePayload
	require.NoError(t, json.Unmarshal(encoded.JSON, &raw))
	require.Equal(t, VolumeBackendKindSSHFS, raw.Backend)
	require.NotZero(t, raw.SSHFS.PrivateKeyResultID)
	require.NotZero(t, raw.SSHFS.KnownHostsResultID)
	require.NotZero(t, raw.SSHFS.ServiceHostResultID)

	decodedTyped, err := (&Volume{}).DecodePersistedObject(ctx, srv, 0, nil, encoded.JSON)
	require.NoError(t, err)
	decoded, ok := decodedTyped.(*Volume)
	require.True(t, ok)
	require.Equal(t, VolumeBackendKindSSHFS, decoded.Backend)
	require.Equal(t, vol.SSHFS.Endpoint, decoded.SSHFS.Endpoint)
	require.Equal(t, vol.SSHFS.HostKeyAlias, decoded.SSHFS.HostKeyAlias)
	require.False(t, decoded.SSHFS.InsecureSkipHostKeyCheck)
	require.Equal(t, "private-key", decoded.SSHFS.PrivateKey.Self().NameVal)
	require.Equal(t, "known-hosts", decoded.SSHFS.KnownHosts.Self().NameVal)
	require.Equal(t, "ssh.example.test", decoded.SSHFS.ServiceHost.Self().CustomHostname)
}

func TestEngineVolumePersistedObjectRoundTrip(t *testing.T) {
	t.Parallel()

	vol := &Volume{
		Backend: VolumeBackendKindEngine,
		Engine: &EngineVolumeConfig{
			Name:          "datasets/models",
			Subdir:        "llama/weights",
			LayoutVersion: EngineVolumeLayoutVersion,
		},
	}
	encoded, err := vol.EncodePersistedObject(context.Background(), nil)
	require.NoError(t, err)
	require.NotContains(t, string(encoded.JSON), "/var/lib/dagger")

	var raw persistedVolumePayload
	require.NoError(t, json.Unmarshal(encoded.JSON, &raw))
	require.Equal(t, VolumeBackendKindEngine, raw.Backend)
	require.Equal(t, "datasets/models", raw.Engine.Name)
	require.Equal(t, "llama/weights", raw.Engine.Subdir)
	require.Equal(t, EngineVolumeLayoutVersion, raw.Engine.LayoutVersion)

	decodedTyped, err := (&Volume{}).DecodePersistedObject(context.Background(), nil, 0, nil, encoded.JSON)
	require.NoError(t, err)
	decoded, ok := decodedTyped.(*Volume)
	require.True(t, ok)
	require.Equal(t, vol, decoded)
}

func TestEngineVolumeHasNoDependencies(t *testing.T) {
	t.Parallel()

	vol := &Volume{
		Backend: VolumeBackendKindEngine,
		Engine: &EngineVolumeConfig{
			Name:          "shared/data",
			LayoutVersion: EngineVolumeLayoutVersion,
		},
	}
	deps, err := vol.AttachDependencyResults(context.Background(), nil, func(dagql.AnyResult) (dagql.AnyResult, error) {
		t.Fatal("engine volumes must not attach session resources")
		return nil, nil
	})
	require.NoError(t, err)
	require.Empty(t, deps)
}

func TestValidateEngineVolumeName(t *testing.T) {
	t.Parallel()

	for _, valid := range []string{"data", "datasets/models-v1", "A/0._-"} {
		require.NoError(t, ValidateEngineVolumeName(valid), valid)
	}
	for _, invalid := range []string{
		"", "/data", "data/", "data//models", ".", "..", "data/../models",
		"fs", "data/fs", "-data", "_data", ".data", "data model", "dáta", "data\\model",
	} {
		require.Error(t, ValidateEngineVolumeName(invalid), invalid)
	}
	require.Error(t, ValidateEngineVolumeName(strings.Repeat("a", engineVolumeMaxNameLength+1)))
	require.Error(t, ValidateEngineVolumeName(strings.Repeat("a/", engineVolumeMaxPathLength/2)+"a"))
}

func TestValidateEngineVolumeSubdir(t *testing.T) {
	t.Parallel()

	for _, valid := range []string{"data", "nested/fs", "spaces are valid", "λ/数据"} {
		require.NoError(t, ValidateEngineVolumeSubdir(valid), valid)
	}
	for _, invalid := range []string{
		"", "/data", "data/", "data//nested", ".", "..", "data/../nested", "data/./nested", "nul\x00byte",
	} {
		require.Error(t, ValidateEngineVolumeSubdir(invalid), invalid)
	}
	require.Error(t, ValidateEngineVolumeSubdir(strings.Repeat("a", engineVolumeMaxNameLength+1)))
}

func TestVolumeAttachDependencyResultsAttachesResources(t *testing.T) {
	t.Parallel()

	srv := newCoreDagqlServerForTest(t, &Query{Server: &cacheVolumeTestQueryServer{mockServer: &mockServer{}}})
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Secret]{}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Service]{}))

	privateKey := volumeTestObjectResult(t, srv, "private-key", &Secret{NameVal: "private-key"})
	knownHosts := volumeTestObjectResult(t, srv, "known-hosts", &Secret{NameVal: "known-hosts"})
	serviceHost := volumeTestObjectResult(t, srv, "service-host", &Service{CustomHostname: "ssh.example.test"})
	attachedPrivateKey := volumeTestObjectResult(t, srv, "attached-private-key", &Secret{NameVal: "attached-private-key"})
	attachedKnownHosts := volumeTestObjectResult(t, srv, "attached-known-hosts", &Secret{NameVal: "attached-known-hosts"})
	attachedServiceHost := volumeTestObjectResult(t, srv, "attached-service-host", &Service{CustomHostname: "attached.example.test"})

	vol := &Volume{
		Backend: VolumeBackendKindSSHFS,
		SSHFS: &SSHFSVolumeConfig{
			PrivateKey:  privateKey,
			KnownHosts:  knownHosts,
			ServiceHost: serviceHost,
		},
	}
	seen := 0
	deps, err := vol.AttachDependencyResults(context.Background(), nil, func(res dagql.AnyResult) (dagql.AnyResult, error) {
		seen++
		switch seen {
		case 1:
			require.Same(t, privateKey.Self(), res.(dagql.ObjectResult[*Secret]).Self())
			return attachedPrivateKey, nil
		case 2:
			require.Same(t, knownHosts.Self(), res.(dagql.ObjectResult[*Secret]).Self())
			return attachedKnownHosts, nil
		case 3:
			require.Same(t, serviceHost.Self(), res.(dagql.ObjectResult[*Service]).Self())
			return attachedServiceHost, nil
		default:
			t.Fatalf("unexpected dependency %d", seen)
			return nil, nil
		}
	})
	require.NoError(t, err)
	require.Len(t, deps, 3)
	require.Same(t, attachedPrivateKey.Self(), vol.SSHFS.PrivateKey.Self())
	require.Same(t, attachedKnownHosts.Self(), vol.SSHFS.KnownHosts.Self())
	require.Same(t, attachedServiceHost.Self(), vol.SSHFS.ServiceHost.Self())
}

func TestContainerWithMountedVolumeAddsVolumeMount(t *testing.T) {
	t.Parallel()

	srv := newCoreDagqlServerForTest(t, &Query{Server: &cacheVolumeTestQueryServer{mockServer: &mockServer{}}})
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Volume]{}))
	volume := volumeTestObjectResult(t, srv, "volume", &Volume{
		Backend: VolumeBackendKindSSHFS,
		SSHFS:   &SSHFSVolumeConfig{Endpoint: "sshfs://git@example.com/srv/repo"},
	})
	container := NewContainer(Platform{})
	container.Config.WorkingDir = "/work"
	container.ImageRef = "example.com/image:latest"

	_, err := container.WithMountedVolume(context.Background(), "mnt", volume, true)
	require.NoError(t, err)
	require.Empty(t, container.ImageRef)
	require.Len(t, container.Mounts, 1)
	require.Equal(t, "/work/mnt", container.Mounts[0].Target)
	require.True(t, container.Mounts[0].Readonly)
	require.Same(t, volume.Self(), container.Mounts[0].VolumeSource.Volume.Self())
}

func TestContainerPersistedObjectRoundTripsVolumeMount(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	ctx = dagql.ContextWithCache(ctx, cache)

	query := &Query{Server: &cacheVolumeTestQueryServer{mockServer: &mockServer{}}}
	srv := newCoreDagqlServerForTest(t, query)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Volume]{}))
	volume := volumeTestCachedObjectResult(t, ctx, cache, srv, "volume-session", "volume", &Volume{
		Backend: VolumeBackendKindSSHFS,
		SSHFS: &SSHFSVolumeConfig{
			Endpoint:     "sshfs://git@example.com/srv/repo",
			HostKeyAlias: "example.com",
		},
	})
	volumeID, err := cache.PersistedResultID(volume)
	require.NoError(t, err)

	container := NewContainer(Platform(specs.Platform{OS: "linux", Architecture: "amd64"}))
	_, err = container.WithMountedVolume(ctx, "/mnt/repo", volume, true)
	require.NoError(t, err)

	encoded, err := container.EncodePersistedObject(ctx, cache)
	require.NoError(t, err)
	var raw persistedContainerPayload
	require.NoError(t, json.Unmarshal(encoded.JSON, &raw))
	require.Len(t, raw.Mounts, 1)
	require.Equal(t, persistedContainerMountKindVolume, raw.Mounts[0].Kind)
	require.Equal(t, volumeID, raw.Mounts[0].VolumeSourceResultID)
	require.True(t, raw.Mounts[0].Readonly)

	decodedTyped, err := (&Container{}).DecodePersistedObject(ctx, srv, volumeID, nil, encoded.JSON)
	require.NoError(t, err)
	decoded, ok := decodedTyped.(*Container)
	require.True(t, ok)
	require.Len(t, decoded.Mounts, 1)
	require.Equal(t, "/mnt/repo", decoded.Mounts[0].Target)
	require.True(t, decoded.Mounts[0].Readonly)
	require.Equal(t, "sshfs://git@example.com/srv/repo", decoded.Mounts[0].VolumeSource.Volume.Self().SSHFS.Endpoint)
}

func TestContainerAttachDependencyResultsKindsOwnsVolumeMount(t *testing.T) {
	t.Parallel()

	srv := newCoreDagqlServerForTest(t, &Query{Server: &cacheVolumeTestQueryServer{mockServer: &mockServer{}}})
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Volume]{}))
	volume := volumeTestObjectResult(t, srv, "volume", &Volume{
		Backend: VolumeBackendKindSSHFS,
		SSHFS:   &SSHFSVolumeConfig{Endpoint: "sshfs://git@example.com/srv/repo"},
	})
	attachedVolume := volumeTestObjectResult(t, srv, "attached-volume", &Volume{
		Backend: VolumeBackendKindSSHFS,
		SSHFS:   &SSHFSVolumeConfig{Endpoint: "sshfs://git@example.com/attached"},
	})
	container := NewContainer(Platform{})
	_, err := container.WithMountedVolume(context.Background(), "/mnt/repo", volume, false)
	require.NoError(t, err)

	deps, err := container.AttachDependencyResultsKinds(context.Background(), nil, func(res dagql.AnyResult) (dagql.AnyResult, error) {
		require.Same(t, volume.Self(), res.(dagql.ObjectResult[*Volume]).Self())
		return attachedVolume, nil
	})
	require.NoError(t, err)
	require.Len(t, deps, 1)
	require.True(t, deps[0].Owned)
	require.Same(t, attachedVolume.Self(), deps[0].Result.(dagql.ObjectResult[*Volume]).Self())
	require.Same(t, attachedVolume.Self(), container.Mounts[0].VolumeSource.Volume.Self())
}

func volumeTestCachedObjectResult[T dagql.Typed](
	t *testing.T,
	ctx context.Context,
	cache *dagql.Cache,
	srv *dagql.Server,
	sessionID string,
	op string,
	self T,
) dagql.ObjectResult[T] {
	t.Helper()

	call := volumeTestCall(op, self)
	res, err := cache.GetOrInitCall(ctx, sessionID, srv, &dagql.CallRequest{
		ResultCall:    call,
		IsPersistable: true,
	}, func(context.Context) (dagql.AnyResult, error) {
		return dagql.NewObjectResultForCall(self, srv, call)
	})
	require.NoError(t, err)
	typed, ok := res.(dagql.ObjectResult[T])
	require.True(t, ok)
	return typed
}

func volumeTestObjectResult[T dagql.Typed](t *testing.T, srv *dagql.Server, op string, self T) dagql.ObjectResult[T] {
	t.Helper()

	res, err := dagql.NewObjectResultForCall(self, srv, volumeTestCall(op, self))
	require.NoError(t, err)
	return res
}

func volumeTestCall[T dagql.Typed](op string, self T) *dagql.ResultCall {
	return &dagql.ResultCall{
		Kind:        dagql.ResultCallKindSynthetic,
		SyntheticOp: op,
		Type:        dagql.NewResultCallType(self.Type()),
	}
}
