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

func TestTracedResetDoesNotRebindExportBaseline(t *testing.T) {
	var queries []string
	dag, err := dagger.Connect(t.Context(), dagger.WithConn(agentTestConn{do: func(req *http.Request) (*http.Response, error) {
		var query dagger.Request
		require.NoError(t, json.NewDecoder(req.Body).Decode(&query))
		queries = append(queries, query.Query)
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"data":{"node":{"withoutMessageHistory":{"withModel":{"id":"reset"}}}}}`))}, nil
	}}))
	require.NoError(t, err)
	defer dag.Close()

	a := &sessionAgent{
		session:     &LLMSession{dag: dag, plumbingCtx: t.Context()},
		tracedReset: dagger.Ref[*dagger.LLM](dag, dagger.ID("traced-snapshot")).WithoutMessageHistory(),
		model:       "test-model",
	}
	// An export is allowed to move comparison bookkeeping, never the reset seed.
	a.setLastSynced(dagger.Ref[*dagger.Workspace](dag, dagger.ID("exported-baseline")))
	_, err = a.resetLLM().ID(t.Context())
	require.NoError(t, err)
	require.Len(t, queries, 1)
	require.Contains(t, queries[0], "traced-snapshot")
	require.Contains(t, queries[0], "withoutMessageHistory")
	require.Contains(t, queries[0], "test-model")
	require.NotContains(t, queries[0], "withWorkspace")
	require.NotContains(t, queries[0], "currentWorkspace")
	require.NotContains(t, queries[0], "exported-baseline")
}
func TestComposeAgentsRequiresSnapshot(t *testing.T) {
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
			if tc.captureErr != "" {
				require.ErrorContains(t, err, tc.captureErr)
				require.ErrorContains(t, err, "capture workspace for agent")
				require.False(t, composed)
				require.Zero(t, liveReads)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "composed-agent", id)
			require.True(t, composed)
			require.Zero(t, liveReads)
			require.Empty(t, warnings.String())
		})
	}
}
