package core

import (
	"encoding/json"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

// Routing a payload's parts together decodes it once, and gives each part
// exactly the route and error routing it alone gives, without the write set.
func TestRouteEachPartRoutesAsRoutePartsDoes(t *testing.T) {
	t.Parallel()
	payload := func(lazyJSON string, consumed bool) json.RawMessage {
		raw, err := json.Marshal(persistedContainerPayload{
			Metadata: persistedContainerMetadata{Consumed: consumed, Value: persistedContainerMetadataValue{
				Mounts: []persistedContainerMountPayload{
					{Target: "/a", Kind: persistedContainerMountKindDirectory, Readonly: true},
					{Target: "/b", Kind: persistedContainerMountKindDirectory},
				},
			}},
			Parts: map[dagql.PartKey]persistedContainerPart{
				ContainerPartFS:          {Kind: containerPartPending},
				ContainerPartExecMeta:    {Kind: containerPartPending},
				ContainerPartMount("/a"): {Kind: containerPartPending},
				ContainerPartMount("/b"): {Kind: containerPartPending},
			},
			LazyJSON: json.RawMessage(lazyJSON),
		})
		require.NoError(t, err)
		return raw
	}
	parts := []dagql.PartKey{ContainerPartMetadata, ContainerPartFS, ContainerPartExecMeta, ContainerPartMount("/a"), ContainerPartMount("/b"), ContainerPartMount("/missing")}
	for _, tc := range []struct {
		field, lazyJSON string
		consumed        bool
	}{
		{"import", `{}`, true},
		{"withEnvVariable", `{}`, true},
		{"withEnvVariable", `{}`, false},
		{"withMountedDirectory", `{"target":"/b"}`, true},
		{"withExec", `{}`, true},
		{"withWorkdir", ``, true},
		{"notAnOperation", `{}`, true},
	} {
		v := dagql.PersistedPayloadVisit{
			Call:    &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: tc.field, Receiver: &dagql.ResultCallRef{ResultID: 1}, Type: dagql.NewResultCallType((&Container{}).Type())},
			Payload: payload(tc.lazyJSON, tc.consumed),
		}
		family := foreignFamilyCodec("Container")
		routes, errs := family.RouteEachPart(v, parts)
		require.Len(t, routes, len(parts))
		require.Len(t, errs, len(parts))
		for i, part := range parts {
			want, wantErr := family.RouteParts(v, part)
			want.WriteSet = nil
			require.Equal(t, want, routes[i], "%s %s", tc.field, part)
			if wantErr == nil {
				require.NoError(t, errs[i], "%s %s", tc.field, part)
			} else {
				require.EqualError(t, errs[i], wantErr.Error(), "%s %s", tc.field, part)
			}
		}
	}
	_, errs := foreignFamilyCodec("Container").RouteEachPart(dagql.PersistedPayloadVisit{Payload: json.RawMessage(`{`)}, parts[:2])
	for _, err := range errs {
		require.Error(t, err, "an undecodable payload fails every part")
	}
}

// Routing all of a container's parts together decodes its payload once and
// routes each part without reading every other: four times the mounts must
// allocate well under sixteen times as much, as routing each part on its own
// decode would.
func TestRouteEachPartScalesWithMounts(t *testing.T) {
	allocs := func(n int) float64 {
		v := probeTestMountedFile(n).visit(t)
		outputs, err := foreignFamilyCodec("Container").MapSnapshotParts(v)
		require.NoError(t, err)
		parts := make([]dagql.PartKey, len(outputs))
		for i, out := range outputs {
			parts[i] = out.Address.Part
		}
		return testing.AllocsPerRun(3, func() {
			if _, errs := foreignFamilyCodec("Container").RouteEachPart(v, parts); errs[0] != nil {
				panic(errs[0])
			}
		})
	}
	small, large := allocs(50), allocs(200)
	require.Less(t, large, 6*small, "routing 200 mounts allocated %.0f times, 50 mounts %.0f times", large, small)
}
