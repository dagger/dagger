package daggercmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/sessionwire"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestExposePortParserAndMerge(t *testing.T) {
	t.Parallel()
	frontend := 15432
	result := sessionwire.DetachedUpResult{Services: []sessionwire.DetachedUpService{
		{
			Name: "web", ServiceID: "web-id", Native: true,
			BackendPorts: []sessionwire.DetachedUpPort{
				{Port: 80, Protocol: sessionwire.NetworkProtocolTCP},
				{Port: 443, Protocol: sessionwire.NetworkProtocolTCP},
			},
		},
		{
			Name: "db", ServiceID: "db-id", Native: false,
			PortMappings: []sessionwire.PortForward{{Frontend: &frontend, Backend: 5432, Protocol: sessionwire.NetworkProtocolTCP}},
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
		alter func(*sessionwire.DetachedUpResult)
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
		{name: "saved native udp", alter: func(result *sessionwire.DetachedUpResult) {
			result.Services[0].BackendPorts[0].Protocol = sessionwire.NetworkProtocolUDP
		}, want: "UDP port forwarding"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := result
			input.Services = append([]sessionwire.DetachedUpService(nil), result.Services...)
			input.Services[0].BackendPorts = append([]sessionwire.DetachedUpPort(nil), result.Services[0].BackendPorts...)
			if test.alter != nil {
				test.alter(&input)
			}
			_, err := normalizeExposeRequest(input, test.specs)
			require.ErrorContains(t, err, test.want)
			var usageErr *exposeUsageError
			if test.name == "explicit udp" {
				require.ErrorAs(t, err, &usageErr)
			}
			if test.name == "saved native udp" {
				require.NotErrorAs(t, err, &usageErr)
			}
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
			OwnerClientID: "publisher",
			Ports:         []engine.SessionPort{{Port: 8080, Backend: 80, Protocol: "TCP"}},
		},
	}, Attachment: &engine.SessionAttachment{ClientID: "publisher"}}
	request := exposeRequest{Mappings: []exposePortMapping{{
		Service: "web", ServiceID: "web-id", Frontend: &frontend, Backend: 80, Protocol: sessionwire.NetworkProtocolTCP,
	}}}
	ports, err := exposedPortsFromDescriptor(descriptor, request)
	require.NoError(t, err)
	require.Equal(t, []exposedPort{{
		Service: "web", Frontend: 8080, Backend: 80, Protocol: sessionwire.NetworkProtocolTCP,
	}}, ports)
}

func TestExposedPortsIgnoreStaleAttachmentOwner(t *testing.T) {
	t.Parallel()
	frontend := 9090
	backendKey := engine.SessionServiceKey{Digest: "backend", SessionID: testCLIValidSessionID(), RuntimeKind: "container"}
	oldTunnel := engine.SessionService{
		Key:            engine.SessionServiceKey{Digest: "old", ClientID: "old-client", RuntimeKind: "tunnel"},
		TunnelUpstream: &backendKey, OwnerClientID: "old-client",
		Ports: []engine.SessionPort{{Port: 8080, Backend: 80, Protocol: "TCP"}},
	}
	newTunnel := engine.SessionService{
		Key:            engine.SessionServiceKey{Digest: "new", ClientID: "new-client", RuntimeKind: "tunnel"},
		TunnelUpstream: &backendKey, OwnerClientID: "new-client",
		Ports: []engine.SessionPort{{Port: frontend, Backend: 80, Protocol: "TCP"}},
	}
	descriptor := engine.SessionDescriptor{
		Attachment: &engine.SessionAttachment{ClientID: "new-client"},
		Services: []engine.SessionService{
			{Key: backendKey, Names: []string{"web"}, Retained: true}, oldTunnel, newTunnel,
		},
	}
	request := exposeRequest{Mappings: []exposePortMapping{{
		Service: "web", Frontend: &frontend, Backend: 80, Protocol: sessionwire.NetworkProtocolTCP,
	}}}
	ports, err := exposedPortsFromDescriptor(descriptor, request)
	require.NoError(t, err)
	require.Equal(t, []exposedPort{{
		Service: "web", Frontend: frontend, Backend: 80, Protocol: sessionwire.NetworkProtocolTCP,
	}}, ports)

	descriptor.Services = descriptor.Services[:2]
	_, err = exposedPortsFromDescriptor(descriptor, request)
	require.ErrorContains(t, err, "not yet available from attachment client new-client")
}

func TestExposedPortsMatchSharedAliasesByBackendInReversedOrder(t *testing.T) {
	t.Parallel()
	backendKey := engine.SessionServiceKey{
		Digest: "shared-backend", SessionID: testCLIValidSessionID(), RuntimeKind: "container",
	}
	descriptor := engine.SessionDescriptor{
		Attachment: &engine.SessionAttachment{ClientID: "publisher"},
		Services: []engine.SessionService{
			{Key: backendKey, Names: []string{"admin", "web"}, Retained: true},
			{
				Key:            engine.SessionServiceKey{Digest: "tunnel", ClientID: "publisher", RuntimeKind: "tunnel"},
				TunnelUpstream: &backendKey, OwnerClientID: "publisher",
				Ports: []engine.SessionPort{
					{Port: 45432, Backend: 5432, Protocol: "TCP"},
					{Port: 48080, Backend: 8080, Protocol: "TCP"},
				},
			},
		},
	}
	request := exposeRequest{Mappings: []exposePortMapping{
		{Service: "web", Backend: 8080, Protocol: sessionwire.NetworkProtocolTCP},
		{Service: "admin", Backend: 5432, Protocol: sessionwire.NetworkProtocolTCP},
	}}

	ports, err := exposedPortsFromDescriptor(descriptor, request)
	require.NoError(t, err)
	require.Equal(t, []exposedPort{
		{Service: "admin", Frontend: 45432, Backend: 5432, Protocol: sessionwire.NetworkProtocolTCP},
		{Service: "web", Frontend: 48080, Backend: 8080, Protocol: sessionwire.NetworkProtocolTCP},
	}, ports)
	require.Equal(t, "tcp://localhost:45432", ports[0].URL())
	require.Equal(t, "http://localhost:48080", ports[1].URL())
}

func TestAttachmentHolderDiagnostics(t *testing.T) {
	t.Parallel()
	cause := &exposeChildError{Code: engine.SessionErrorAlreadyAttached, Message: "already attached"}
	for _, test := range []struct {
		name  string
		ready bool
	}{
		{name: "zero-port starting holder", ready: false},
		{name: "sessions attach holder", ready: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			descriptor := engine.SessionDescriptor{Attachment: &engine.SessionAttachment{
				ClientID: "holder-client", Hostname: "builder.example", Ready: test.ready,
			}}
			err := attachmentHolderConflict(descriptor, testCLIValidSessionID(), cause)
			require.ErrorContains(t, err, "session is attached by client holder-client on builder.example")
			require.NotContains(t, err.Error(), "ports are being served")
		})
	}

	descriptor := engine.SessionDescriptor{Attachment: &engine.SessionAttachment{
		ClientID: "holder-client", Hostname: "builder.example", Ready: true,
	}}
	descriptor.Services = []engine.SessionService{{
		TunnelUpstream: &engine.SessionServiceKey{}, OwnerClientID: "holder-client",
		Ports: []engine.SessionPort{{Port: 8080}},
	}}
	err := attachmentHolderConflict(descriptor, testCLIValidSessionID(), cause)
	require.ErrorContains(t, err, "ports are being served by client holder-client on builder.example")

	descriptor.Services = []engine.SessionService{{
		TunnelUpstream: &engine.SessionServiceKey{}, OwnerClientID: "old-client",
		Ports: []engine.SessionPort{{Port: 8080}},
	}}
	err = attachmentHolderConflict(descriptor, testCLIValidSessionID(), cause)
	require.ErrorContains(t, err, "session is attached by client holder-client on builder.example")
	require.NotContains(t, err.Error(), "ports are being served")
}

func TestLateAttachmentConflictReinspectsCurrentHolder(t *testing.T) {
	t.Parallel()
	control := &fakeExposeSessionControl{descriptors: []engine.SessionDescriptor{{
		Attachment: &engine.SessionAttachment{ClientID: "late-holder"},
	}}}
	cause := &exposeChildError{Code: engine.SessionErrorAlreadyAttached, Message: "already attached"}
	err := inspectAttachmentHolderConflict(t.Context(), control, testCLIValidSessionID(), nil, cause)
	require.ErrorContains(t, err, "session is attached by client late-holder")
	require.Equal(t, 1, control.inspectCalls)
}

func TestAttachmentTransportFailureNamesOnlySameObservedHolder(t *testing.T) {
	t.Parallel()
	initial := &engine.SessionAttachment{
		ID: "att_aaaaaaaaaaaaaaaaaaaaaaaaaa", Generation: 2, ClientID: "initial-holder",
	}
	cause := errors.New("attach transport failed")

	t.Run("same holder", func(t *testing.T) {
		control := &fakeExposeSessionControl{descriptors: []engine.SessionDescriptor{{Attachment: initial}}}
		err := inspectAttachmentHolderConflict(t.Context(), control, testCLIValidSessionID(), initial, cause)
		require.ErrorContains(t, err, "session is attached by client initial-holder")
	})
	t.Run("holder changed", func(t *testing.T) {
		control := &fakeExposeSessionControl{descriptors: []engine.SessionDescriptor{{Attachment: &engine.SessionAttachment{
			ID: "att_bbbbbbbbbbbbbbbbbbbbbbbbbb", Generation: 3, ClientID: "new-holder",
		}}}}
		err := inspectAttachmentHolderConflict(t.Context(), control, testCLIValidSessionID(), initial, cause)
		require.ErrorIs(t, err, cause)
		require.NotContains(t, err.Error(), "new-holder")
	})
}

func TestExposeStopValidatesSessionKindAndCleansMissingSessionState(t *testing.T) {
	t.Parallel()
	sessionID := testCLIValidSessionID()
	for _, test := range []struct {
		name       string
		descriptor engine.SessionDescriptor
		inspectErr error
		wantErr    string
	}{
		{name: "missing query", descriptor: engine.SessionDescriptor{}, wantErr: "session has no up services"},
		{name: "non-up query", descriptor: engine.SessionDescriptor{Query: &engine.SessionQuery{Presentation: engine.QueryPresentation{Kind: "call"}}}, wantErr: "session has no up services"},
		{name: "running up", descriptor: engine.SessionDescriptor{Query: &engine.SessionQuery{Status: engine.SessionQueryStateRunning, Presentation: engine.QueryPresentation{Kind: "up"}}}},
		{name: "failed up", descriptor: engine.SessionDescriptor{Query: &engine.SessionQuery{Status: engine.SessionQueryStateFailed, Presentation: engine.QueryPresentation{Kind: "up"}}}},
		{name: "not found", inspectErr: &client.SessionProtocolError{StatusCode: http.StatusNotFound, Code: engine.SessionErrorSessionNotFound, Message: "gone"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			stateDir, err := os.MkdirTemp("/tmp", "du-stop-")
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, os.RemoveAll(stateDir)) })
			paths, err := makeExposePaths(stateDir, sessionID)
			require.NoError(t, err)
			require.NoError(t, writeExposeRecord(paths.Record, exposeRecord{State: exposeStateReady}))
			require.NoError(t, os.WriteFile(paths.Socket, []byte("stale"), 0o600))
			control := &fakeExposeSessionControl{descriptors: []engine.SessionDescriptor{test.descriptor}, inspectErr: test.inspectErr}
			cmd := &cobra.Command{}
			var output bytes.Buffer
			cmd.SetOut(&output)
			err = exposeDetachableSessionWithControl(t.Context(), cmd, control, sessionID, stateDir, nil, false, true)
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
				_, statErr := os.Stat(paths.Record)
				require.NoError(t, statErr, "validation failure cleaned local state")
				return
			}
			require.NoError(t, err)
			require.Contains(t, output.String(), "Stopped local ports")
			_, statErr := os.Stat(paths.Record)
			require.ErrorIs(t, statErr, os.ErrNotExist)
			_, statErr = os.Stat(paths.Socket)
			require.ErrorIs(t, statErr, os.ErrNotExist)
		})
	}
}

func TestSessionsExposeCloudGuardAllowsLocalStop(t *testing.T) {
	oldCloud, oldRunnerHost := useCloudEngine, RunnerHost
	t.Cleanup(func() {
		useCloudEngine, RunnerHost = oldCloud, oldRunnerHost
	})
	useCloudEngine = true
	RunnerHost = "docker-container://local"

	stateDir, err := os.MkdirTemp("/tmp", "du-cloud-stop-")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(stateDir)) })
	sessionID := testCLIValidSessionID()
	paths, err := makeExposePaths(stateDir, sessionID)
	require.NoError(t, err)
	require.NoError(t, writeExposeRecord(paths.Record, exposeRecord{State: exposeStateReady}))
	require.NoError(t, os.WriteFile(paths.Socket, []byte("stale"), 0o600))
	control := &fakeExposeSessionControl{descriptors: []engine.SessionDescriptor{{
		Query: &engine.SessionQuery{Presentation: engine.QueryPresentation{Kind: "up"}},
	}}}
	cmd := &cobra.Command{}
	var output bytes.Buffer
	cmd.SetOut(&output)
	proceedCalls := 0
	err = withSessionsExposeEngineTarget(true, func() error {
		proceedCalls++
		return exposeDetachableSessionWithControl(
			t.Context(), cmd, control, sessionID, stateDir, nil, false, true,
		)
	})
	require.NoError(t, err)
	require.Equal(t, 1, proceedCalls)
	require.Contains(t, output.String(), "Stopped local ports")
	_, err = os.Stat(paths.Record)
	require.ErrorIs(t, err, os.ErrNotExist)

	proceedCalls = 0
	err = withSessionsExposeEngineTarget(false, func() error {
		proceedCalls++
		return errors.New("publish path reached connection")
	})
	require.ErrorContains(t, err, "sessions expose is not supported with a Dagger Cloud Engine")
	require.Zero(t, proceedCalls, "Cloud publish reached connection or spawn path")
}

type fakeExposeSessionControl struct {
	descriptors  []engine.SessionDescriptor
	inspectErr   error
	inspectCalls int
}

func (control *fakeExposeSessionControl) InspectSession(context.Context, string) (engine.SessionDescriptor, error) {
	control.inspectCalls++
	if control.inspectErr != nil {
		return engine.SessionDescriptor{}, control.inspectErr
	}
	if len(control.descriptors) == 0 {
		return engine.SessionDescriptor{}, errors.New("unexpected inspect")
	}
	descriptor := control.descriptors[0]
	if len(control.descriptors) > 1 {
		control.descriptors = control.descriptors[1:]
	}
	return descriptor, nil
}

func (*fakeExposeSessionControl) InspectPrimaryQuery(context.Context, string) (engine.SessionQuery, error) {
	return engine.SessionQuery{}, errors.New("unexpected primary query inspection")
}

func (*fakeExposeSessionControl) PrimaryQueryResult(context.Context, string) (client.SessionResult, error) {
	return client.SessionResult{}, errors.New("unexpected primary query result")
}

func TestPublishExposePortsStartsBeforeReadingPorts(t *testing.T) {
	t.Parallel()
	frontend := 8080
	request := exposeRequest{Mappings: []exposePortMapping{{
		Service: "web", ServiceID: "backend-id", Frontend: &frontend,
		Backend: 80, Protocol: sessionwire.NetworkProtocolTCP,
	}}}
	fake := &fakeExposeQueryClient{t: t}
	ports, err := publishExposePorts(t.Context(), fake, request)
	require.NoError(t, err)
	require.Equal(t, []exposedPort{{
		Service: "web", Frontend: 8080, Backend: 80, Protocol: sessionwire.NetworkProtocolTCP,
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
