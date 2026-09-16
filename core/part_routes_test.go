package core

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func TestPartPureRoutes(t *testing.T) {
	ctx := context.Background()
	metadata := persistedContainerMetadataValue{Platform: Platform{OS: "linux", Architecture: "amd64"}, Mounts: []persistedContainerMountPayload{
		{Target: "/data", Kind: persistedContainerMountKindDirectory},
		{Target: "/readonly", Kind: persistedContainerMountKindFile, Readonly: true},
	}}
	ctr, err := containerRoutingMetadata(metadata)
	require.NoError(t, err)
	path := dagql.PersistedRefPath{}.Field("items").Index(2)
	cases := []struct {
		field  string
		recipe any
		native LazyContainerParts
	}{
		{"withExec", persistedContainerExecLazy{}, &ContainerExecLazy{}},
		{"withExec", persistedContainerExecLazy{VolatileCacheHitParentResultID: 99}, &ContainerVolatileExecCacheHitLazy{}},
		{"from", struct{}{}, &ContainerFromImageRefLazy{}},
		{"withRootfs", struct{}{}, &ContainerWithRootFSLazy{}},
		{"withWorkdir", struct{}{}, &ContainerWithWorkdirLazy{}},
		{"withDirectory", persistedContainerWithDirectoryLazy{Path: "/data/child"}, &ContainerWithDirectoryLazy{Path: "/data/child"}},
		{"withMountedFile", persistedContainerWithMountedFileLazy{Target: "/readonly"}, &ContainerWithMountedFileLazy{Target: "/readonly"}},
		{"withSymlink", persistedContainerWithSymlinkLazy{LinkPath: "/data/link"}, &ContainerWithSymlinkLazy{LinkPath: "/data/link"}},
	}
	for _, test := range cases {
		t.Run(test.field, func(t *testing.T) {
			raw, err := json.Marshal(test.recipe)
			require.NoError(t, err)
			payload, err := json.Marshal(persistedContainerPayload{Metadata: persistedContainerMetadata{Consumed: true, Value: metadata}, LazyJSON: raw})
			require.NoError(t, err)
			visit := dagql.PersistedPayloadVisit{Path: path, Call: &dagql.ResultCall{Field: test.field}, Payload: payload}
			for _, part := range append([]dagql.PartKey{ContainerPartMetadata}, containerSnapshotParts(ctr)...) {
				route, err := foreignFamilyCodec("Container").RouteParts(visit, part)
				require.NoError(t, err)
				expected, err := test.native.ContainerLazyGroups(ctx, ctr, []dagql.PartKey{part})
				require.NoError(t, err)
				require.Equal(t, []dagql.LazyGroupKey{route.Group.Group}, expected)
				require.Equal(t, path, route.Group.OutputPath)
				require.Contains(t, route.WriteSet, dagql.PersistedPartAddress{OutputPath: path, Part: part})
			}
		})
	}
	for _, field := range []string{"_builtinContainer", "import"} {
		payload, err := json.Marshal(persistedContainerPayload{Metadata: persistedContainerMetadata{Consumed: true, Value: metadata}, LazyJSON: json.RawMessage(`{}`)})
		require.NoError(t, err)
		route, err := foreignFamilyCodec("Container").RouteParts(dagql.PersistedPayloadVisit{Path: path, Call: &dagql.ResultCall{Field: field}, Payload: payload}, ContainerPartFS)
		require.NoError(t, err)
		require.Equal(t, dagql.LazyGroupWhole, route.Group.Group)
		require.Len(t, route.WriteSet, 5)
	}
	for kind := range persistedDirectoryLazyVisitors {
		raw, err := json.Marshal(persistedDirectoryPayload{LazyKind: kind, Platform: metadata.Platform})
		require.NoError(t, err)
		route, err := foreignFamilyCodec("Directory").RouteParts(dagql.PersistedPayloadVisit{Path: path, Payload: raw}, "snapshot")
		require.NoError(t, err)
		require.True(t, route.HasProducer)
		require.Equal(t, []dagql.PersistedPartAddress{{OutputPath: path, Part: "snapshot"}}, route.WriteSet)
	}
	for kind := range persistedFileLazyVisitors {
		raw, err := json.Marshal(persistedFilePayload{LazyKind: kind, Platform: metadata.Platform})
		require.NoError(t, err)
		route, err := foreignFamilyCodec("File").RouteParts(dagql.PersistedPayloadVisit{Path: path, Payload: raw}, "snapshot")
		require.NoError(t, err)
		require.True(t, route.HasProducer)
		require.Equal(t, []dagql.PersistedPartAddress{{OutputPath: path, Part: "snapshot"}}, route.WriteSet)
	}
}
