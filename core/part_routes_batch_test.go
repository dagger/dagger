package core

import (
	"encoding/json"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

// Routing a payload's parts together decodes it once, and gives each part
// exactly the route and error routing it alone gives.
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
