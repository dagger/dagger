package daggercmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/slog"
	"github.com/stretchr/testify/require"
)

type agentTestConn struct {
	do func(*http.Request) (*http.Response, error)
}

func (c agentTestConn) Host() string { return "agent-test" }
func (c agentTestConn) Close() error { return nil }
func (c agentTestConn) Do(req *http.Request) (*http.Response, error) {
	return c.do(req)
}

func TestComposeAgentsSnapshotFallback(t *testing.T) {
	for _, tc := range []struct {
		name       string
		captureErr string
		cancel     bool
	}{
		{name: "snapshot succeeds"},
		{name: "large untracked file", captureErr: "worktree content exceeds the configured per-file bound"},
		{name: "too many untracked files", captureErr: "untracked content exceeds the configured aggregate bounds"},
		{name: "dirty submodule", captureErr: "capture rejected a changed submodule"},
		{name: "cancelled", cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var warnings bytes.Buffer
			ctx, cancel := context.WithCancel(slog.WithLogger(t.Context(), slog.New(slog.NewTextHandler(&warnings, nil))))
			defer cancel()
			wantWorkspace := "captured-workspace"
			if tc.captureErr != "" {
				wantWorkspace = "current-workspace"
			}
			snapshots, liveReads, composed := 0, 0, false
			dag, err := dagger.Connect(ctx, dagger.WithConn(agentTestConn{do: func(req *http.Request) (*http.Response, error) {
				var query dagger.Request
				require.NoError(t, json.NewDecoder(req.Body).Decode(&query))
				var payload any
				switch {
				case strings.Contains(query.Query, "snapshot"):
					snapshots++
					if tc.cancel {
						cancel()
						return nil, context.Canceled
					}
					if tc.captureErr != "" {
						payload = map[string]any{"errors": []map[string]string{{"message": tc.captureErr}}}
					} else {
						payload = map[string]any{"data": map[string]any{"currentWorkspace": map[string]any{"snapshot": map[string]string{"id": wantWorkspace}}}}
					}
				case query.OpName == "ComposeAgents":
					composed = true
					vars := query.Variables.(map[string]any)
					require.Equal(t, wantWorkspace, vars["workspace"])
					require.Equal(t, []any{"editor"}, vars["include"])
					payload = map[string]any{"data": map[string]any{"workspace": map[string]any{"agents": map[string]any{"compose": map[string]string{"id": "composed-agent"}}}}}
				case strings.Contains(query.Query, "currentWorkspace"):
					liveReads++
					payload = map[string]any{"data": map[string]any{"currentWorkspace": map[string]string{"id": "current-workspace"}}}
				default:
					require.Contains(t, query.Query, "node")
					payload = map[string]any{"data": map[string]any{"node": map[string]string{"id": "captured-workspace"}}}
				}
				body, err := json.Marshal(payload)
				require.NoError(t, err)
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(body))}, nil
			}}))
			require.NoError(t, err)
			defer dag.Close()
			id, err := composeAgents(ctx, dag, []string{"editor"})
			require.Equal(t, 1, snapshots)
			if tc.cancel {
				require.ErrorIs(t, err, context.Canceled)
				require.False(t, composed)
				require.Zero(t, liveReads)
				require.Empty(t, warnings.String())
				return
			}
			require.NoError(t, err)
			require.Equal(t, "composed-agent", id)
			require.True(t, composed)
			if tc.captureErr != "" {
				require.Equal(t, 1, liveReads)
				require.Contains(t, warnings.String(), "level=WARN")
				require.Contains(t, warnings.String(), tc.captureErr)
				require.Contains(t, warnings.String(), "continuing with the live workspace")
			} else {
				require.Zero(t, liveReads)
				require.Empty(t, warnings.String())
			}
		})
	}
}
