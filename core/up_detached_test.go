package core

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

func detachedUpServiceResultForTest(
	t *testing.T,
	name string,
	ports []Port,
) dagql.ObjectResult[*Service] {
	t.Helper()
	query := &Query{Server: &mockServer{}}
	dag := newCoreDagqlServerForTest(t, query)
	dag.InstallObject(dagql.NewClass(dag, dagql.ClassOpts[*Container]{}))
	dag.InstallObject(dagql.NewClass(dag, dagql.ClassOpts[*Service]{}))
	cache, err := dagql.NewCache(t.Context(), "", nil, nil)
	require.NoError(t, err)
	ctx := dagql.ContextWithCache(t.Context(), cache)
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		SessionID: "detached-up-test-" + name,
		ClientID:  "detached-up-client",
	})
	container := &Container{Ports: ports}
	containerDetached, err := dagql.NewObjectResultForCall(container, dag, &dagql.ResultCall{
		Kind:        dagql.ResultCallKindSynthetic,
		SyntheticOp: "detached_up_container_" + name,
		Type:        dagql.NewResultCallType(container.Type()),
	})
	require.NoError(t, err)
	containerAny, err := cache.AttachResult(ctx, "detached-up-test-"+name, dag, containerDetached)
	require.NoError(t, err)
	containerResult, ok := containerAny.(dagql.ObjectResult[*Container])
	require.True(t, ok)
	service := &Service{Container: containerResult}
	serviceDetached, err := dagql.NewObjectResultForCall(service, dag, &dagql.ResultCall{
		Kind:        dagql.ResultCallKindSynthetic,
		SyntheticOp: "detached_up_service_" + name,
		Type:        dagql.NewResultCallType(service.Type()),
	})
	require.NoError(t, err)
	serviceAny, err := cache.AttachResult(ctx, "detached-up-test-"+name, dag, serviceDetached)
	require.NoError(t, err)
	serviceResult, ok := serviceAny.(dagql.ObjectResult[*Service])
	require.True(t, ok)
	return serviceResult
}

func TestPrepareDetachedUpServiceExactPayloadAndRoundTrip(t *testing.T) {
	t.Parallel()

	t.Run("native", func(t *testing.T) {
		service := detachedUpServiceResultForTest(t, "native", []Port{{
			Port: 8080, Protocol: NetworkProtocolTCP,
		}})
		payload, err := prepareDetachedUpService("web", nil, service)
		require.NoError(t, err)
		serviceID, err := service.ID()
		require.NoError(t, err)
		encodedID, err := serviceID.Encode()
		require.NoError(t, err)

		encoded, err := json.Marshal(DetachedUpResult{Services: []DetachedUpService{payload}})
		require.NoError(t, err)
		require.Equal(t, fmt.Sprintf(
			`{"services":[{"name":"web","serviceId":%q,"native":true,"portMappings":[],"backendPorts":[{"port":8080,"protocol":"TCP"}]}]}`,
			encodedID,
		), string(encoded))

		var roundTrip DetachedUpResult
		require.NoError(t, json.Unmarshal(encoded, &roundTrip))
		require.Len(t, roundTrip.Services, 1)
		require.True(t, roundTrip.Services[0].Native)
		require.Empty(t, roundTrip.Services[0].PortMappings)
		require.Equal(t, []DetachedUpPort{{Port: 8080, Protocol: NetworkProtocolTCP}}, roundTrip.Services[0].BackendPorts)
	})

	t.Run("fixed mapping", func(t *testing.T) {
		service := detachedUpServiceResultForTest(t, "fixed", []Port{{
			Port: 5432, Protocol: NetworkProtocolTCP,
		}})
		frontend := 15432
		payload, err := prepareDetachedUpService("db", []PortForward{{
			Frontend: &frontend, Backend: 5432, Protocol: NetworkProtocolTCP,
		}}, service)
		require.NoError(t, err)
		require.False(t, payload.Native)
		require.Equal(t, &frontend, payload.PortMappings[0].Frontend)

		encoded, err := json.Marshal(DetachedUpResult{Services: []DetachedUpService{payload}})
		require.NoError(t, err)
		var roundTrip DetachedUpResult
		require.NoError(t, json.Unmarshal(encoded, &roundTrip))
		require.Equal(t, &frontend, roundTrip.Services[0].PortMappings[0].Frontend)
		require.Equal(t, 5432, roundTrip.Services[0].PortMappings[0].Backend)
		require.Equal(t, NetworkProtocolTCP, roundTrip.Services[0].PortMappings[0].Protocol)
	})

	t.Run("mapping to undeclared backend", func(t *testing.T) {
		service := detachedUpServiceResultForTest(t, "undeclared", nil)
		frontend := 18080
		payload, err := prepareDetachedUpService("web", []PortForward{{
			Frontend: &frontend, Backend: 8080, Protocol: NetworkProtocolTCP,
		}}, service)
		require.NoError(t, err)
		require.Empty(t, payload.BackendPorts)
		require.Equal(t, 8080, payload.PortMappings[0].Backend)
	})
}

func TestPrepareDetachedUpServiceParityRejections(t *testing.T) {
	t.Parallel()

	t.Run("non-container", func(t *testing.T) {
		_, err := prepareDetachedUpService("host", nil, dagql.ObjectResult[*Service]{})
		require.ErrorContains(t, err, "tunneling to non-Container services is not supported")
	})

	t.Run("empty effective ports", func(t *testing.T) {
		service := detachedUpServiceResultForTest(t, "empty", nil)
		_, err := prepareDetachedUpService("empty", nil, service)
		require.ErrorContains(t, err, "no ports to forward")
	})

	t.Run("native UDP", func(t *testing.T) {
		service := detachedUpServiceResultForTest(t, "native-udp", []Port{{
			Port: 5353, Protocol: NetworkProtocolUDP,
		}})
		_, err := prepareDetachedUpService("dns", nil, service)
		require.ErrorContains(t, err, "UDP port forwarding is not supported")
	})

	t.Run("configured UDP", func(t *testing.T) {
		service := detachedUpServiceResultForTest(t, "configured-udp", nil)
		frontend := 5353
		_, err := prepareDetachedUpService("dns", []PortForward{{
			Frontend: &frontend, Backend: 5353, Protocol: NetworkProtocolUDP,
		}}, service)
		require.ErrorContains(t, err, "UDP port forwarding is not supported")
	})

	t.Run("random declaration", func(t *testing.T) {
		service := detachedUpServiceResultForTest(t, "random", nil)
		_, err := prepareDetachedUpService("web", []PortForward{{
			Backend: 8080, Protocol: NetworkProtocolTCP,
		}}, service)
		require.ErrorContains(t, err, "must have a fixed frontend")
	})
}

func TestDetachedUpCollisionCheckUsesEffectiveFrontends(t *testing.T) {
	t.Parallel()
	err := checkUpPortCollisions([]upServicePort{
		{name: "web", port: upPortKey{port: 8080, protocol: NetworkProtocolTCP}},
		{name: "api", port: upPortKey{port: 8080, protocol: NetworkProtocolTCP}},
	})
	require.ErrorContains(t, err, `port 8080/tcp is exposed by both "web" and "api"`)
	require.NoError(t, checkUpPortCollisions([]upServicePort{
		{name: "web", port: upPortKey{port: 8080, protocol: NetworkProtocolTCP}},
		{name: "dns", port: upPortKey{port: 8080, protocol: NetworkProtocolUDP}},
	}))
}
