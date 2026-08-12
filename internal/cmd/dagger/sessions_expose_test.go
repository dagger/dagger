package daggercmd

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

func TestExposePortParserAndMerge(t *testing.T) {
	t.Parallel()
	frontend := 15432
	result := core.DetachedUpResult{Services: []core.DetachedUpService{
		{
			Name: "web", ServiceID: "web-id", Native: true,
			BackendPorts: []core.DetachedUpPort{
				{Port: 80, Protocol: core.NetworkProtocolTCP},
				{Port: 443, Protocol: core.NetworkProtocolTCP},
			},
		},
		{
			Name: "db", ServiceID: "db-id", Native: false,
			PortMappings: []core.PortForward{{Frontend: &frontend, Backend: 5432, Protocol: core.NetworkProtocolTCP}},
		},
	}}

	t.Run("defaults", func(t *testing.T) {
		request, err := normalizeExposeRequest(result, nil)
		require.NoError(t, err)
		require.Len(t, request.Mappings, 3)
		require.Equal(t, 15432, *mappingFor(t, request, "db", 5432).Frontend)
		require.Equal(t, 80, *mappingFor(t, request, "web", 80).Frontend)
		require.Equal(t, 443, *mappingFor(t, request, "web", 443).Frontend)
	})

	t.Run("native override and addition", func(t *testing.T) {
		request, err := normalizeExposeRequest(result, []string{"web=:80", "web=9443:443", "web=9000:9000"})
		require.NoError(t, err)
		require.Len(t, request.Mappings, 4)
		require.Nil(t, mappingFor(t, request, "web", 80).Frontend)
		require.Equal(t, 9443, *mappingFor(t, request, "web", 443).Frontend)
		require.Equal(t, 9000, *mappingFor(t, request, "web", 9000).Frontend)
	})

	t.Run("last duplicate wins", func(t *testing.T) {
		request, err := normalizeExposeRequest(result, []string{"db=1000:5432", "db=:5432"})
		require.NoError(t, err)
		require.Nil(t, mappingFor(t, request, "db", 5432).Frontend)
		encoded, err := json.Marshal(request)
		require.NoError(t, err)
		var decoded exposeRequest
		require.NoError(t, json.Unmarshal(encoded, &decoded))
		require.Nil(t, mappingFor(t, decoded, "db", 5432).Frontend, "random frontend did not survive normalization and decode")
	})

	tests := []struct {
		name  string
		specs []string
		alter func(*core.DetachedUpResult)
		want  string
	}{
		{name: "missing equals", specs: []string{"web=80"}, want: "expected SERVICE"},
		{name: "missing service", specs: []string{"=8080:80"}, want: "expected SERVICE"},
		{name: "unknown service", specs: []string{"cache=8080:80"}, want: "unknown up service"},
		{name: "frontend range", specs: []string{"web=70000:80"}, want: "1..65535"},
		{name: "backend range", specs: []string{"web=8080:0"}, want: "1..65535"},
		{name: "bad protocol", specs: []string{"web=8080:80/sctp"}, want: "invalid protocol"},
		{name: "explicit udp", specs: []string{"web=8080:80/udp"}, want: "UDP port forwarding"},
		{name: "merged collision", specs: []string{"db=80:5432"}, want: "requested by both"},
		{name: "saved native udp", alter: func(result *core.DetachedUpResult) {
			result.Services[0].BackendPorts[0].Protocol = core.NetworkProtocolUDP
		}, want: "UDP port forwarding"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := result
			input.Services = append([]core.DetachedUpService(nil), result.Services...)
			input.Services[0].BackendPorts = append([]core.DetachedUpPort(nil), result.Services[0].BackendPorts...)
			if test.alter != nil {
				test.alter(&input)
			}
			_, err := normalizeExposeRequest(input, test.specs)
			require.ErrorContains(t, err, test.want)
		})
	}
}

func TestExposeStopFlagExclusivityIsUsageError(t *testing.T) {
	t.Parallel()
	cmd := newSessionsExposeCommand()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{testCLIValidSessionID(), "--stop", "--port", "web=8080:80"})
	err := cmd.Execute()
	var exitErr idtui.ExitError
	require.ErrorAs(t, err, &exitErr)
	require.Equal(t, 2, exitErr.OriginalCode)
	require.Contains(t, output.String(), "--stop cannot be combined")
}

func TestExposedPortsFromSessionDescriptor(t *testing.T) {
	t.Parallel()
	frontend := 8080
	backendKey := engine.SessionServiceKey{Digest: "backend", SessionID: testCLIValidSessionID(), RuntimeKind: "container"}
	descriptor := engine.SessionDescriptor{Services: []engine.SessionService{
		{Key: backendKey, Names: []string{"web"}, Kind: "container", Retained: true},
		{
			Key:  engine.SessionServiceKey{Digest: "tunnel", SessionID: testCLIValidSessionID(), ClientID: "publisher", RuntimeKind: "tunnel"},
			Kind: "tunnel", TunnelUpstream: &backendKey,
			Ports: []engine.SessionPort{{Port: 8080, Protocol: "TCP"}},
		},
	}}
	request := exposeRequest{Mappings: []exposePortMapping{{
		Service: "web", ServiceID: "web-id", Frontend: &frontend, Backend: 80, Protocol: core.NetworkProtocolTCP,
	}}}
	ports := exposedPortsFromDescriptor(descriptor, request)
	require.Equal(t, []exposedPort{{
		Service: "web", Frontend: 8080, Backend: 80, Protocol: core.NetworkProtocolTCP,
	}}, ports)
}

func TestPublishExposePortsStartsBeforeReadingPorts(t *testing.T) {
	t.Parallel()
	frontend := 8080
	request := exposeRequest{Mappings: []exposePortMapping{{
		Service: "web", ServiceID: "backend-id", Frontend: &frontend,
		Backend: 80, Protocol: core.NetworkProtocolTCP,
	}}}
	fake := &fakeExposeQueryClient{t: t}
	ports, err := publishExposePorts(t.Context(), fake, request)
	require.NoError(t, err)
	require.Equal(t, []exposedPort{{
		Service: "web", Frontend: 8080, Backend: 80, Protocol: core.NetworkProtocolTCP,
	}}, ports)
	require.Equal(t, []string{"StartExposeService", "ExposeServicePorts"}, fake.operations)
}

type fakeExposeQueryClient struct {
	t          *testing.T
	operations []string
}

func (fake *fakeExposeQueryClient) Do(
	_ context.Context,
	query string,
	operation string,
	variables map[string]any,
	data any,
) error {
	fake.t.Helper()
	fake.operations = append(fake.operations, operation)
	var response string
	switch operation {
	case "StartExposeService":
		require.NotContains(fake.t, query, "start {")
		require.Equal(fake.t, "backend-id", variables["service"])
		response = `{"host":{"tunnel":{"start":"tunnel-id"}}}`
	case "ExposeServicePorts":
		require.True(fake.t, strings.Contains(query, "... on Service"))
		require.Equal(fake.t, "tunnel-id", variables["service"])
		response = `{"node":{"ports":[{"port":8080,"protocol":"TCP"}]}}`
	default:
		fake.t.Fatalf("unexpected operation %q", operation)
	}
	return json.Unmarshal([]byte(response), data)
}

func mappingFor(t *testing.T, request exposeRequest, service string, backend int) exposePortMapping {
	t.Helper()
	for _, mapping := range request.Mappings {
		if mapping.Service == service && mapping.Backend == backend {
			return mapping
		}
	}
	t.Fatalf("mapping %s:%d not found", service, backend)
	return exposePortMapping{}
}
