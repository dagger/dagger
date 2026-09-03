package schema

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/distribution/reference"
	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

type containerImagePartsConcurrencyTestOp struct {
	core.LazyState
	fsBodyHook func()
}

func (op *containerImagePartsConcurrencyTestOp) Evaluate(ctx context.Context, ctr *core.Container) error {
	if err := op.EvaluateContainerGroup(ctx, ctr, core.ContainerLazyGroupMetadata); err != nil {
		return err
	}
	if err := op.EvaluateContainerGroup(ctx, ctr, core.ContainerLazyGroupWrite); err != nil {
		return err
	}
	ctr.Lazy = nil
	return nil
}

func (op *containerImagePartsConcurrencyTestOp) AttachDependencies(context.Context, func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	return nil, nil
}

func (op *containerImagePartsConcurrencyTestOp) EncodePersisted(context.Context, dagql.PersistedObjectCache) (json.RawMessage, error) {
	return nil, nil
}

func (op *containerImagePartsConcurrencyTestOp) ContainerLazyGroups(_ context.Context, _ *core.Container, parts []dagql.PartKey) ([]dagql.LazyGroupKey, error) {
	if parts == nil {
		return []dagql.LazyGroupKey{core.ContainerLazyGroupMetadata, core.ContainerLazyGroupWrite}, nil
	}
	groups := make([]dagql.LazyGroupKey, 0, len(parts))
	seen := map[dagql.LazyGroupKey]struct{}{}
	for _, part := range parts {
		group := core.ContainerLazyGroupWrite
		if part == core.ContainerPartMetadata {
			group = core.ContainerLazyGroupMetadata
		}
		if _, ok := seen[group]; ok {
			continue
		}
		seen[group] = struct{}{}
		groups = append(groups, group)
	}
	return groups, nil
}

func (op *containerImagePartsConcurrencyTestOp) EvaluateContainerGroup(ctx context.Context, _ *core.Container, group dagql.LazyGroupKey) error {
	return op.LazyState.EvaluateGroup(ctx, "test.containerImagePartsConcurrency", group, func(context.Context) error {
		if group == core.ContainerLazyGroupWrite && op.fsBodyHook != nil {
			op.fsBodyHook()
		}
		return nil
	})
}

func attachContainerInternalTestObject[T dagql.Typed](
	t *testing.T,
	ctx context.Context,
	cache *dagql.Cache,
	srv *dagql.Server,
	sessionID string,
	syntheticOp string,
	self T,
) dagql.ObjectResult[T] {
	t.Helper()
	frame := &dagql.ResultCall{
		Kind:        dagql.ResultCallKindSynthetic,
		SyntheticOp: syntheticOp,
		Type:        dagql.NewResultCallType(self.Type()),
	}
	res, err := cache.GetOrInitCall(ctx, sessionID, srv, &dagql.CallRequest{ResultCall: frame}, func(context.Context) (dagql.AnyResult, error) {
		return dagql.NewObjectResultForCall(self, srv, frame)
	})
	require.NoError(t, err)
	return res.(dagql.ObjectResult[T])
}

func TestEvaluateContainerImagePartsRunsContainersConcurrently(t *testing.T) {
	const sessionID = "container-image-parts-concurrency-session"
	ctx := engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{
		ClientID:  "container-image-parts-concurrency-client",
		SessionID: sessionID,
	})
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, cache.CloseDiscardingPersistence())
	})
	ctx = dagql.ContextWithCache(ctx, cache)
	srv, err := dagql.NewServer(ctx, &core.Query{})
	require.NoError(t, err)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*core.Container]{Typed: &core.Container{}}))

	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()

	containers := make([]dagql.ObjectResult[*core.Container], 0, 2)
	for _, syntheticOp := range []string{"image-parts-concurrency-a", "image-parts-concurrency-b"} {
		op := &containerImagePartsConcurrencyTestOp{
			LazyState: core.NewLazyState(),
			fsBodyHook: func() {
				entered <- struct{}{}
				<-release
			},
		}
		ctr := core.NewContainer(core.Platform{})
		ctr.Lazy = op
		containers = append(containers, attachContainerInternalTestObject(t, ctx, cache, srv, sessionID, syntheticOp, ctr))
	}

	done := make(chan error, 1)
	go func() {
		done <- evaluateContainerImageParts(ctx, cache, containers...)
	}()

	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for range containers {
		select {
		case <-entered:
		case err := <-done:
			t.Fatalf("image part evaluation returned before both rootfs bodies entered: %v", err)
		case <-timer.C:
			t.Fatal("timed out waiting for concurrent rootfs evaluation")
		}
	}
	unblock()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-timer.C:
		t.Fatal("timed out waiting for image part evaluation")
	}
	for _, ctr := range containers {
		require.Nil(t, ctr.Self().Lazy)
	}
}

func TestShouldSelectLatestImageRelease(t *testing.T) {
	t.Parallel()

	bare, err := reference.ParseNormalizedNamed("alpine")
	require.NoError(t, err)
	explicitLatest, err := reference.ParseNormalizedNamed("alpine:latest")
	require.NoError(t, err)

	for _, tc := range []struct {
		name    string
		version string
		ref     reference.Named
		want    bool
	}{
		{name: "legacy bare address", version: "v1.0.0-beta.9", ref: bare},
		{name: "locking without latest release", version: workspaceLockingVersion, ref: bare},
		{name: "new bare address", version: workspace.LatestReleaseVersion, ref: bare, want: true},
		{name: "explicit latest", version: workspace.LatestReleaseVersion, ref: explicitLatest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := dagql.ContextWithCall(t.Context(), &dagql.ResultCall{
				View: call.View(tc.version),
			})
			require.Equal(t, tc.want, shouldSelectLatestImageRelease(
				ctx,
				tc.ref,
			))
		})
	}
}

func TestContainerDefaultPlatformCacheIdentity(t *testing.T) {
	ctx := context.Background()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, cache.Close(context.Background()))
	})
	ctx = dagql.ContextWithCache(ctx, cache)
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		ClientID:  "container-platform-client",
		SessionID: "container-platform-session",
	})

	amd64 := core.Platform{OS: "linux", Architecture: "amd64"}
	arm64 := core.Platform{OS: "linux", Architecture: "arm64"}
	srv := &currentTypeDefsTestServer{platform: amd64}
	root := core.NewRoot(srv)
	coreSchemaBase, err := NewCoreSchemaBase(ctx, srv)
	require.NoError(t, err)
	dag, err := coreSchemaBase.Fork(ctx, root, "")
	require.NoError(t, err)
	srv.dag = dag

	selectContainer := func(platform dagql.Input) dagql.ObjectResult[*core.Container] {
		t.Helper()
		selector := dagql.Selector{Field: "container"}
		if platform != nil {
			selector.Args = []dagql.NamedInput{{Name: "platform", Value: platform}}
		}
		var ctr dagql.ObjectResult[*core.Container]
		require.NoError(t, dag.Select(ctx, dag.Root(), &ctr, selector))
		return ctr
	}
	recipeDigest := func(ctr dagql.ObjectResult[*core.Container]) string {
		t.Helper()
		id, err := ctr.RecipeID(ctx)
		require.NoError(t, err)
		return id.Digest().String()
	}

	omittedAMD64 := selectContainer(nil)
	require.Equal(t, amd64, omittedAMD64.Self().Platform)

	explicitAMD64 := selectContainer(dagql.Opt(amd64))
	require.Equal(t, recipeDigest(omittedAMD64), recipeDigest(explicitAMD64))

	srv.platform = arm64
	omittedARM64 := selectContainer(nil)
	require.Equal(t, arm64, omittedARM64.Self().Platform)
	require.NotEqual(t, recipeDigest(omittedAMD64), recipeDigest(omittedARM64))

	explicitAMD64OnARM64 := selectContainer(dagql.Opt(amd64))
	require.Equal(t, amd64, explicitAMD64OnARM64.Self().Platform)
	require.Equal(t, recipeDigest(explicitAMD64), recipeDigest(explicitAMD64OnARM64))

	nullOnARM64 := selectContainer(dagql.NoOpt[core.Platform]())
	require.Equal(t, arm64, nullOnARM64.Self().Platform)
	require.Equal(t, recipeDigest(omittedARM64), recipeDigest(nullOnARM64))
}

func TestCloneContainerForSchemaChildDisablesFromContentDigest(t *testing.T) {
	t.Parallel()

	dag, err := dagql.NewServer(t.Context(), &core.Query{})
	require.NoError(t, err)
	dag.InstallObject(dagql.NewClass(dag, dagql.ClassOpts[*core.Container]{Typed: &core.Container{}}))

	parent, err := dagql.NewObjectResultForCall(core.NewContainer(core.Platform{}), dag, &dagql.ResultCall{
		Kind:        dagql.ResultCallKindSynthetic,
		SyntheticOp: "scratch-container",
		Type:        dagql.NewResultCallType((&core.Container{}).Type()),
	})
	require.NoError(t, err)
	require.True(t, parent.Self().CanUseFromContentDigest())

	child, _, err := cloneContainerForSchemaChild(t.Context(), parent)
	require.NoError(t, err)
	require.False(t, child.CanUseFromContentDigest())
}

func TestEagerContainerMountMetadataResolvers(t *testing.T) {
	const sessionID = "eager-container-mount-resolvers-session"
	ctx := engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{
		ClientID:  "eager-container-mount-resolvers-client",
		SessionID: sessionID,
	})
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, cache.CloseDiscardingPersistence())
	})
	ctx = dagql.ContextWithCache(ctx, cache)
	queryServer := &currentTypeDefsTestServer{}
	query := core.NewRoot(queryServer)
	dag, err := dagql.NewServer(ctx, query)
	require.NoError(t, err)
	queryServer.dag = dag
	ctx = core.ContextWithQuery(ctx, query)
	dag.InstallObject(dagql.NewClass(dag, dagql.ClassOpts[*core.Container]{Typed: &core.Container{}}))
	dag.InstallObject(dagql.NewClass(dag, dagql.ClassOpts[*core.Volume]{Typed: &core.Volume{}}))
	dag.InstallObject(dagql.NewClass(dag, dagql.ClassOpts[*core.Secret]{Typed: &core.Secret{}}))
	dag.InstallObject(dagql.NewClass(dag, dagql.ClassOpts[*core.Socket]{Typed: &core.Socket{}}))
	schema := &containerSchema{}

	t.Run("without mount", func(t *testing.T) {
		parentSelf := core.NewContainer(core.Platform{})
		parentSelf.ImageRef = "parent-image"
		parentSelf.Mounts = core.ContainerMounts{{
			Target:      "/old",
			TmpfsSource: &core.TmpfsMountSource{},
		}}
		parent, err := dagql.NewObjectResultForCall(parentSelf, dag, &dagql.ResultCall{
			Kind:        dagql.ResultCallKindSynthetic,
			SyntheticOp: "eager-without-mount-parent",
			Type:        dagql.NewResultCallType((&core.Container{}).Type()),
		})
		require.NoError(t, err)

		child, err := schema.withoutMount(ctx, parent, containerWithoutMountArgs{Path: "/old"})
		require.NoError(t, err)
		require.Nil(t, child.Lazy)
		require.Empty(t, child.Mounts)
		require.Empty(t, child.ImageRef)
	})

	t.Run("mounted temp", func(t *testing.T) {
		parent, err := dagql.NewObjectResultForCall(core.NewContainer(core.Platform{}), dag, &dagql.ResultCall{
			Kind:        dagql.ResultCallKindSynthetic,
			SyntheticOp: "eager-mounted-temp-parent",
			Type:        dagql.NewResultCallType((&core.Container{}).Type()),
		})
		require.NoError(t, err)

		child, err := schema.withMountedTemp(ctx, parent, containerWithMountedTempArgs{Path: "/tmp"})
		require.NoError(t, err)
		require.Nil(t, child.Lazy)
		require.Len(t, child.Mounts, 1)
		require.Equal(t, "/tmp", child.Mounts[0].Target)
		require.NotNil(t, child.Mounts[0].TmpfsSource)
	})

	t.Run("mounted volume", func(t *testing.T) {
		volume := attachContainerInternalTestObject(t, ctx, cache, dag, sessionID, "eager-mounted-volume-source", &core.Volume{})
		volumeID, err := volume.ID()
		require.NoError(t, err)
		parent, err := dagql.NewObjectResultForCall(core.NewContainer(core.Platform{}), dag, &dagql.ResultCall{
			Kind:        dagql.ResultCallKindSynthetic,
			SyntheticOp: "eager-mounted-volume-parent",
			Type:        dagql.NewResultCallType((&core.Container{}).Type()),
		})
		require.NoError(t, err)

		child, err := schema.withMountedVolume(ctx, parent, containerWithMountedVolumeArgs{
			Path:     "/volume",
			Volume:   dagql.NewID[*core.Volume](volumeID),
			ReadOnly: true,
		})
		require.NoError(t, err)
		require.Nil(t, child.Lazy)
		require.Len(t, child.Mounts, 1)
		require.Equal(t, "/volume", child.Mounts[0].Target)
		require.True(t, child.Mounts[0].Readonly)
		require.Same(t, volume.Self(), child.Mounts[0].VolumeSource.Volume.Self())
	})

	t.Run("mounted secret without owner", func(t *testing.T) {
		secret := attachContainerInternalTestObject(t, ctx, cache, dag, sessionID, "eager-mounted-secret-source", &core.Secret{NameVal: "test"})
		secretID, err := secret.ID()
		require.NoError(t, err)
		parent, err := dagql.NewObjectResultForCall(core.NewContainer(core.Platform{}), dag, &dagql.ResultCall{
			Kind:        dagql.ResultCallKindSynthetic,
			SyntheticOp: "eager-mounted-secret-parent",
			Type:        dagql.NewResultCallType((&core.Container{}).Type()),
		})
		require.NoError(t, err)

		child, err := schema.withMountedSecret(ctx, parent, containerWithMountedSecretArgs{
			Path:   "/secret",
			Source: dagql.NewID[*core.Secret](secretID),
			Mode:   0o400,
		})
		require.NoError(t, err)
		require.Nil(t, child.Lazy)
		require.Len(t, child.Secrets, 1)
		require.Equal(t, "/secret", child.Secrets[0].MountPath)
		require.Nil(t, child.Secrets[0].Owner)
		require.Same(t, secret.Self(), child.Secrets[0].Secret.Self())
	})

	t.Run("unix socket without owner", func(t *testing.T) {
		socket := attachContainerInternalTestObject(t, ctx, cache, dag, sessionID, "eager-unix-socket-source", &core.Socket{Kind: core.SocketKindUnixOpaque})
		socketID, err := socket.ID()
		require.NoError(t, err)
		parent, err := dagql.NewObjectResultForCall(core.NewContainer(core.Platform{}), dag, &dagql.ResultCall{
			Kind:        dagql.ResultCallKindSynthetic,
			SyntheticOp: "eager-unix-socket-parent",
			Type:        dagql.NewResultCallType((&core.Container{}).Type()),
		})
		require.NoError(t, err)

		child, err := schema.withUnixSocket(ctx, parent, containerWithUnixSocketArgs{
			Path:   "/socket",
			Source: dagql.NewID[*core.Socket](socketID),
		})
		require.NoError(t, err)
		require.Nil(t, child.Lazy)
		require.Len(t, child.Sockets, 1)
		require.Equal(t, "/socket", child.Sockets[0].ContainerPath)
		require.Nil(t, child.Sockets[0].Owner)
		require.Same(t, socket.Self(), child.Sockets[0].Source.Self())
	})

	t.Run("without unix socket", func(t *testing.T) {
		parentSelf := core.NewContainer(core.Platform{})
		parentSelf.ImageRef = "parent-image"
		parentSelf.Sockets = []core.ContainerSocket{{ContainerPath: "/old.sock"}}
		parent, err := dagql.NewObjectResultForCall(parentSelf, dag, &dagql.ResultCall{
			Kind:        dagql.ResultCallKindSynthetic,
			SyntheticOp: "eager-without-unix-socket-parent",
			Type:        dagql.NewResultCallType((&core.Container{}).Type()),
		})
		require.NoError(t, err)

		child, err := schema.withoutUnixSocket(ctx, parent, containerWithoutUnixSocketArgs{Path: "/old.sock"})
		require.NoError(t, err)
		require.Nil(t, child.Lazy)
		require.Empty(t, child.Sockets)
		require.Empty(t, child.ImageRef)
	})
}

func TestWithImageConfigMetadataMutatesContainerConfig(t *testing.T) {
	t.Parallel()

	s := &containerSchema{}
	ctx := context.Background()

	healthcheck := dockerspec.HealthcheckConfig{
		Test:          []string{"CMD-SHELL", "test -f /out.txt"},
		Interval:      21 * time.Second,
		Timeout:       4 * time.Second,
		StartPeriod:   9 * time.Second,
		StartInterval: 2 * time.Second,
		Retries:       5,
	}
	healthcheckJSON, err := json.Marshal(&healthcheck)
	require.NoError(t, err)

	parent := &core.Container{
		Config: dockerspec.DockerOCIImageConfig{
			ImageConfig: ocispecs.ImageConfig{
				Volumes: map[string]struct{}{"/old": {}},
			},
			DockerOCIImageConfigExt: dockerspec.DockerOCIImageConfigExt{
				OnBuild: []string{"RUN old"},
				Shell:   []string{"/bin/sh", "-c"},
			},
		},
	}

	updated, err := s.withImageConfigMetadata(ctx, parent, containerWithImageConfigMetadataArgs{
		Healthcheck: string(healthcheckJSON),
		OnBuild: dagql.Opt(
			dagql.ArrayInput[dagql.String]{
				dagql.NewString("RUN child-build"),
			},
		),
		Shell: dagql.Opt(
			dagql.ArrayInput[dagql.String]{
				dagql.NewString("/bin/ash"),
				dagql.NewString("-eo"),
				dagql.NewString("pipefail"),
				dagql.NewString("-c"),
			},
		),
		Volumes: dagql.Opt(
			dagql.ArrayInput[dagql.String]{
				dagql.NewString("/cache"),
				dagql.NewString("/data"),
				dagql.NewString("/cache"),
			},
		),
		StopSignal: "SIGQUIT",
	})
	require.NoError(t, err)
	require.NotNil(t, updated)
	require.Same(t, parent, updated)

	require.NotNil(t, parent.Config.Healthcheck)
	require.Equal(t, healthcheck, *parent.Config.Healthcheck)
	require.Equal(t, []string{"RUN child-build"}, parent.Config.OnBuild)
	require.Equal(t, []string{"/bin/ash", "-eo", "pipefail", "-c"}, parent.Config.Shell)
	require.Equal(t, map[string]struct{}{"/cache": {}, "/data": {}}, parent.Config.Volumes)
	require.Equal(t, "SIGQUIT", parent.Config.StopSignal)
}

func TestWithImageConfigMetadataRejectsInvalidHealthcheckJSON(t *testing.T) {
	t.Parallel()

	s := &containerSchema{}
	ctx := context.Background()

	_, err := s.withImageConfigMetadata(ctx, &core.Container{}, containerWithImageConfigMetadataArgs{
		Healthcheck: "{this is not json}",
	})
	require.Error(t, err)
	require.ErrorContains(t, err, "failed to decode healthcheck metadata")
}
