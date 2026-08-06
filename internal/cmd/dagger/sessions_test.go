package daggercmd

import (
	"bytes"
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
	for _, name := range []string{"list", "inspect", "attach", "terminate"} {
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
}

func TestSessionListAndInspectOutput(t *testing.T) {
	t.Parallel()
	created := time.Date(2026, 8, 5, 14, 3, 12, 0, time.UTC)
	descriptor := engine.SessionDescriptor{
		ID: testCLIValidSessionID(), State: engine.SessionStateDetached, CreatedAt: created,
		Query: &engine.SessionQuery{
			ID: "qry_aaaaaaaaaaaaaaaaaaaaaaaaaa", Status: engine.SessionQueryStateRunning,
		},
	}
	var output bytes.Buffer
	require.NoError(t, writeSessionsTable(&output, []engine.SessionDescriptor{descriptor}))
	require.Contains(t, output.String(), descriptor.ID)
	require.Contains(t, output.String(), "detached")
	require.Contains(t, output.String(), "running")

	output.Reset()
	require.NoError(t, writeSessionInspect(&output, descriptor))
	require.Contains(t, output.String(), "ID")
	require.Contains(t, output.String(), "State")
	require.Contains(t, output.String(), "Attached   -")
}

func TestSessionsHelpJSONAndEmptyOutput(t *testing.T) {
	t.Parallel()
	cmd := newSessionsCommand()
	var help bytes.Buffer
	cmd.SetOut(&help)
	cmd.SetErr(&help)
	cmd.SetArgs([]string{"--help"})
	require.NoError(t, cmd.Execute())
	for _, expected := range []string{"Manage detachable sessions", "list", "inspect", "attach", "terminate"} {
		require.Contains(t, help.String(), expected)
	}

	var empty bytes.Buffer
	require.NoError(t, writeSessionsTable(&empty, nil))
	require.Equal(t, "ID   STATE   QUERY   STATUS   CREATED   ATTACHED\n", empty.String())

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

func testCLIValidSessionID() string {
	return "sess_aaaaaaaaaaaaaaaaaaaaaaaaaa"
}
