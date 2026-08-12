package daggercmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestDetachedUpQueryRequest(t *testing.T) {
	t.Parallel()
	request, err := buildDetachedUpQueryRequest(dagger.ID("up-group-id"))
	require.NoError(t, err)
	var decoded struct {
		Query         string         `json:"query"`
		OperationName string         `json:"operationName"`
		Variables     map[string]any `json:"variables"`
	}
	require.NoError(t, json.Unmarshal(request, &decoded))
	require.Equal(t, "DetachedUp", decoded.OperationName)
	require.Contains(t, decoded.Query, "node(id: $id)")
	require.Contains(t, decoded.Query, "... on UpGroup { _startDetached }")
	require.Equal(t, "up-group-id", decoded.Variables["id"])
}

func TestDetachedUpSummary(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	sessionID := testCLIValidSessionID()
	require.NoError(t, writeDetachedUpSummary(&output, sessionID, []exposedPort{
		{Service: "web", Frontend: 8080, Backend: 80},
		{Service: "db", Frontend: 15432, Backend: 5432},
	}, "/state/session.log"))
	require.Equal(t, "\nDetached session "+sessionID+"\n"+
		"  web  http://localhost:8080\n"+
		"  db  tcp://localhost:15432\n"+
		"Ports served by a background process on this machine (log: /state/session.log)\n\n"+
		"Inspect:    dagger sessions inspect "+sessionID+"\n"+
		"Re-expose:  dagger sessions expose "+sessionID+"\n"+
		"Terminate:  dagger sessions terminate "+sessionID+"\n", output.String())
	require.Error(t, writeDetachedUpSummary(failingWriter{}, sessionID, nil, "log"))
}

func TestDetachedUpListFlagConflictIsUsageError(t *testing.T) {
	oldDetach, oldList := upDetachMode, upListMode
	t.Cleanup(func() { upDetachMode, upListMode = oldDetach, oldList })
	upDetachMode, upListMode = true, true
	cmd := &cobra.Command{Use: "up"}
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	err := upCmd.RunE(cmd, nil)
	var exitErr idtui.ExitError
	require.ErrorAs(t, err, &exitErr)
	require.Equal(t, 2, exitErr.OriginalCode)
	require.Contains(t, output.String(), "--detach cannot be combined with --list")
	require.NotNil(t, upCmd.Flags().Lookup("detach"))
}

func TestDetachedUpRejectsEffectiveCloudEngineBeforeConnect(t *testing.T) {
	oldDetach, oldList := upDetachMode, upListMode
	oldCloud, oldRunnerHost := useCloudEngine, RunnerHost
	t.Cleanup(func() {
		upDetachMode, upListMode = oldDetach, oldList
		useCloudEngine, RunnerHost = oldCloud, oldRunnerHost
	})
	upDetachMode, upListMode = true, false
	cmd := &cobra.Command{Use: "up"}

	useCloudEngine = true
	RunnerHost = "docker-container://local"
	err := upCmd.RunE(cmd, nil)
	require.ErrorContains(t, err, "background port server cannot reconnect")

	useCloudEngine = false
	RunnerHost = engine.DefaultCloudRunnerHost
	err = upCmd.RunE(cmd, nil)
	require.ErrorContains(t, err, "background port server cannot reconnect")

	RunnerHost = "docker-container://local"
	require.NoError(t, validateDetachedUpEngineTarget())
}

func TestDetachedUpRollbackOwnership(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		withChild bool
		committed bool
		wantAbort int
		wantTerm  int
	}{
		{name: "canceled while polling after acknowledgment", wantTerm: 1},
		{name: "creator closed before child ready", wantTerm: 1},
		{name: "child ready before commit", withChild: true, wantAbort: 1, wantTerm: 1},
		{name: "summary failure after commit", withChild: true, committed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			abortCalls, terminateCalls := 0, 0
			transaction := &detachedUpTransaction{
				acknowledged: func() bool { return true },
				committed:    test.committed,
				terminate: func(ctx context.Context) error {
					terminateCalls++
					require.NoError(t, ctx.Err(), "rollback inherited a canceled command context")
					deadline, ok := ctx.Deadline()
					require.True(t, ok)
					require.WithinDuration(t, time.Now().Add(70*time.Second), deadline, time.Second)
					if test.withChild {
						require.Equal(t, 1, abortCalls, "session terminated before the uncommitted child aborted")
					}
					return nil
				},
			}
			if test.withChild {
				transaction.abort = func() error {
					abortCalls++
					return nil
				}
			}
			require.NoError(t, transaction.rollback())
			require.Equal(t, test.wantAbort, abortCalls)
			require.Equal(t, test.wantTerm, terminateCalls)
		})
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("summary write failed") }
