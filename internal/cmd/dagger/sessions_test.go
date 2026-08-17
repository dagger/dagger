package daggercmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

func TestSessionsCommandTree(t *testing.T) {
	t.Parallel()
	cmd := newSessionsCommand()
	require.Equal(t, "sessions", cmd.Name())
	for _, name := range []string{"list", "inspect", "attach", "expose", "terminate"} {
		child, _, err := cmd.Find([]string{name})
		require.NoError(t, err)
		require.Equal(t, name, child.Name())
	}
	list, _, err := cmd.Find([]string{"list"})
	require.NoError(t, err)
	require.NotNil(t, list.Flags().Lookup("json"))
	inspect, _, err := cmd.Find([]string{"inspect"})
	require.NoError(t, err)
	require.NotNil(t, inspect.Flags().Lookup("json"))
	terminate, _, err := cmd.Find([]string{"terminate"})
	require.NoError(t, err)
	require.NotNil(t, terminate.Flags().Lookup("json"))
	attach, _, err := cmd.Find([]string{"attach"})
	require.NoError(t, err)
	require.Equal(t, "true", attach.Annotations[showFinalProgressKey])
	expose, _, err := cmd.Find([]string{"expose"})
	require.NoError(t, err)
	for _, flag := range []string{"port", "replace", "stop"} {
		require.NotNil(t, expose.Flags().Lookup(flag))
	}
}

func TestSessionListAndInspectOutput(t *testing.T) {
	t.Parallel()
	created := time.Date(2026, 8, 5, 14, 3, 12, 0, time.UTC)
	backendKey := engine.SessionServiceKey{Digest: "backend", SessionID: testCLIValidSessionID(), RuntimeKind: "container"}
	descriptor := engine.SessionDescriptor{
		ID: testCLIValidSessionID(), State: engine.SessionStateDetached, CreatedAt: created,
		Query: &engine.SessionQuery{
			ID: "qry_aaaaaaaaaaaaaaaaaaaaaaaaaa", Status: engine.SessionQueryStateSucceeded,
			Presentation: engine.QueryPresentation{Kind: "up"},
		},
		Attachment: &engine.SessionAttachment{ID: "att_aaaaaaaaaaaaaaaaaaaaaaaaaa", ClientID: "publisher", Hostname: "erikbox"},
		Services: []engine.SessionService{
			{
				Key: backendKey, Names: []string{"web"}, Kind: "container", Retained: true,
				Ports: []engine.SessionPort{{Port: 80, Protocol: "TCP"}},
			},
			{
				Key:  engine.SessionServiceKey{Digest: "tunnel", SessionID: testCLIValidSessionID(), RuntimeKind: "tunnel", ClientID: "publisher"},
				Kind: "tunnel", TunnelUpstream: &backendKey,
				OwnerClientID: "publisher", OwnerClientHostname: "erikbox",
				Ports: []engine.SessionPort{{Port: 8080, Protocol: "TCP"}},
			},
		},
	}
	var output bytes.Buffer
	require.NoError(t, writeSessionsTable(&output, []engine.SessionDescriptor{descriptor}))
	require.Contains(t, output.String(), descriptor.ID)
	require.Contains(t, output.String(), "detached")
	require.Contains(t, output.String(), "succeeded")
	require.Contains(t, output.String(), "SERVICES")
	require.Contains(t, output.String(), "PORTS")
	require.Contains(t, output.String(), "1          1")

	output.Reset()
	require.NoError(t, writeSessionInspect(&output, descriptor))
	require.Contains(t, output.String(), "ID")
	require.Contains(t, output.String(), "State")
	require.Contains(t, output.String(), "Attached   att_aaaaaaaaaaaaaaaaaaaaaaaaaa (publisher on erikbox)")
	require.Contains(t, output.String(), "qry_aaaaaaaaaaaaaaaaaaaaaaaaaa (up, succeeded)")
	require.Contains(t, output.String(), "Services")
	require.Contains(t, output.String(), "web   container   running   declared 80/tcp")
	require.Contains(t, output.String(), "Published ports")
	require.Contains(t, output.String(), "0.0.0.0:8080/tcp   → web   (served from erikbox)")

	descriptor.Attachment.Hostname = ""
	output.Reset()
	require.NoError(t, writeSessionInspect(&output, descriptor))
	require.Contains(t, output.String(), "Attached   att_aaaaaaaaaaaaaaaaaaaaaaaaaa (publisher)")
}

func TestSessionsHelpJSONAndEmptyOutput(t *testing.T) {
	t.Parallel()
	cmd := newSessionsCommand()
	var help bytes.Buffer
	cmd.SetOut(&help)
	cmd.SetErr(&help)
	cmd.SetArgs([]string{"--help"})
	require.NoError(t, cmd.Execute())
	for _, expected := range []string{"Manage detachable sessions", "list", "inspect", "attach", "expose", "terminate"} {
		require.Contains(t, help.String(), expected)
	}

	var empty bytes.Buffer
	require.NoError(t, writeSessionsTable(&empty, nil))
	require.Equal(t, "ID   STATE   QUERY   STATUS   SERVICES   PORTS   CREATED   ATTACHED\n", empty.String())

	descriptor := engine.SessionDescriptor{
		ID: testCLIValidSessionID(), State: engine.SessionStateDetached,
		CreatedAt: time.Date(2026, 8, 5, 14, 3, 12, 0, time.UTC),
	}
	var encoded bytes.Buffer
	require.NoError(t, writeSessionsJSON(&encoded, descriptor))
	var decoded engine.SessionDescriptor
	require.NoError(t, json.Unmarshal(encoded.Bytes(), &decoded))
	require.Equal(t, descriptor, decoded)
	require.Contains(t, encoded.String(), "\n    \"id\"")
}

func TestMalformedSessionCLIArgumentReturnsUsageExit(t *testing.T) {
	t.Parallel()
	cmd := newSessionsInspectCommand()
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)
	err := validateSessionCLIArg(cmd, "not-a-session")
	var exitErr idtui.ExitError
	require.ErrorAs(t, err, &exitErr)
	require.Equal(t, 2, exitErr.OriginalCode)
	require.True(t, errors.Is(err, exitErr.Original))
	require.Contains(t, stderr.String(), "malformed session ID")
	require.Contains(t, stderr.String(), "--help")
}

func TestDetachedQueryResultStateBeforeFetch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		query     engine.SessionQuery
		wantError string
	}{
		{name: "succeeded", query: engine.SessionQuery{Status: engine.SessionQueryStateSucceeded}},
		{name: "failed may have saved result", query: engine.SessionQuery{Status: engine.SessionQueryStateFailed}},
		{name: "canceled", query: engine.SessionQuery{Status: engine.SessionQueryStateCanceled}, wantError: "detached query was canceled"},
		{name: "discarded", query: engine.SessionQuery{Status: engine.SessionQueryStateResultDiscarded}, wantError: "detached query result was discarded"},
		{name: "running", query: engine.SessionQuery{Status: engine.SessionQueryStateRunning}, wantError: "detached query finished with status running"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateDetachedQueryResultState(test.query)
			if test.wantError == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, test.wantError)
		})
	}
}

func TestPrimaryQueryCompletionPolling(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		states     []engine.SessionQueryState
		queryError string
		wantStatus engine.SessionQueryState
		wantError  string
		wantWaits  int
	}{
		{name: "succeeded", states: []engine.SessionQueryState{engine.SessionQueryStateRunning, engine.SessionQueryStateSucceeded}, wantStatus: engine.SessionQueryStateSucceeded, wantWaits: 1},
		{name: "failed", states: []engine.SessionQueryState{engine.SessionQueryStateFailed}, wantStatus: engine.SessionQueryStateFailed},
		{name: "canceled", states: []engine.SessionQueryState{engine.SessionQueryStateCanceled}, wantError: "detached query was canceled"},
		{name: "result discarded", states: []engine.SessionQueryState{engine.SessionQueryStateResultDiscarded}, wantError: "detached query result was discarded"},
		{name: "unknown with error", states: []engine.SessionQueryState{"unknown"}, queryError: "saved state error", wantError: "saved state error"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			inspections := 0
			waits := 0
			query, err := pollPrimaryQuery(t.Context(), func(context.Context) (engine.SessionQuery, error) {
				state := test.states[inspections]
				inspections++
				return engine.SessionQuery{Status: state, Error: test.queryError}, nil
			}, func(context.Context) error {
				waits++
				return nil
			})
			if test.wantError != "" {
				require.EqualError(t, err, test.wantError)
			} else {
				require.NoError(t, err)
				require.Equal(t, test.wantStatus, query.Status)
			}
			require.Equal(t, len(test.states), inspections)
			require.Equal(t, test.wantWaits, waits)
		})
	}
}

func TestPrimaryQueryPollingCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(errors.New("poll canceled"))
	_, err := pollPrimaryQuery(ctx, func(context.Context) (engine.SessionQuery, error) {
		return engine.SessionQuery{Status: engine.SessionQueryStateRunning}, nil
	}, func(ctx context.Context) error {
		return context.Cause(ctx)
	})
	require.EqualError(t, err, "poll canceled")
}

func testCLIValidSessionID() string {
	return "sess_aaaaaaaaaaaaaaaaaaaaaaaaaa"
}
