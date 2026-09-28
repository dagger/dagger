package daggercmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/charmbracelet/huh"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/stretchr/testify/require"
)

const changesetApplyTestID = "fixture-changeset"
const changesetApplyTestIDResponse = `{"data":{"node":{"id":"fixture-changeset"}}}`
const changesetApplyTestDiffResponse = `{"data":{"changeset":{"diffStats":[{"path":"result.txt","oldPath":null,"kind":"MODIFIED","addedLines":2,"removedLines":1}]}}}`

type changesetApplyTestRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

type changesetApplyTestReply struct {
	body string
	err  error
}

// WithConn ensures these tests never discover, provision, or contact an engine.
type changesetApplyTestConn struct {
	t        *testing.T
	replies  []changesetApplyTestReply
	requests []changesetApplyTestRequest
}

func (c *changesetApplyTestConn) Host() string { return "fixture.invalid" }
func (c *changesetApplyTestConn) Close() error { return nil }

func (c *changesetApplyTestConn) Do(req *http.Request) (*http.Response, error) {
	c.t.Helper()
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	var query changesetApplyTestRequest
	if err := json.NewDecoder(req.Body).Decode(&query); err != nil {
		return nil, err
	}
	c.requests = append(c.requests, query)
	i := len(c.requests) - 1
	if i >= len(c.replies) {
		c.t.Errorf("unexpected request %d: %s", i, query.Query)
		return nil, errors.New("unexpected GraphQL request")
	}
	reply := c.replies[i]
	if reply.err != nil {
		return nil, reply.err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(reply.body)),
		Request:    req,
	}, nil
}

func newChangesetApplyTestClient(t *testing.T, replies ...changesetApplyTestReply) (*dagger.Client, *changesetApplyTestConn) {
	t.Helper()
	conn := &changesetApplyTestConn{t: t, replies: replies}
	dag, err := dagger.Connect(t.Context(), dagger.WithConn(conn))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, dag.Close()) })
	return dag, conn
}

func requireChangesetApplyPathQuery(t *testing.T, conn *changesetApplyTestConn) {
	t.Helper()
	// Ref is lazy: one existing ID materialization, then one analysis request.
	// The three path fields must not introduce three analysis round trips.
	require.Len(t, conn.requests, 2)
	require.Contains(t, conn.requests[0].Query, "id")
	query := conn.requests[1]
	for _, field := range []string{"addedPaths", "modifiedPaths", "removedPaths"} {
		require.Equal(t, 1, strings.Count(query.Query, field), field)
	}
	require.NotContains(t, query.Query, "diffStats")
	require.NotContains(t, query.Query, "isEmpty")
	require.Equal(t, changesetApplyTestID, query.Variables["changeset"])
}

func TestChangesetAutoApplyPaths(t *testing.T) {
	tests := []struct {
		name    string
		paths   string
		applied bool
	}{
		{"empty", `{"addedPaths":[],"modifiedPaths":[],"removedPaths":[]}`, false},
		{"null node matches preview no-op", `null`, false},
		{"added file", `{"addedPaths":["result.txt"]}`, true},
		{"modified binary", `{"modifiedPaths":["target/dagger/app"]}`, true},
		{"removed file", `{"removedPaths":["old.txt"]}`, true},
		{"added empty directory", `{"addedPaths":["empty/"]}`, true},
		{"removed empty directory", `{"removedPaths":["gone/"]}`, true},
		{"collapsed removed subtree", `{"removedPaths":["gone/"]}`, true},
		{"rename", `{"addedPaths":["new.txt"],"removedPaths":["old.txt"]}`, true},
		{"all path kinds", `{"addedPaths":["new.txt"],"modifiedPaths":["keep.txt"],"removedPaths":["old.txt"]}`, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dag, conn := newChangesetApplyTestClient(t,
				changesetApplyTestReply{body: changesetApplyTestIDResponse},
				changesetApplyTestReply{body: `{"data":{"changeset":` + test.paths + `}}`},
			)
			changeset := dagger.Ref[*dagger.Changeset](dag, dagger.ID(changesetApplyTestID))
			var preview bytes.Buffer
			calls := 0
			applied, err := handleChangesetResponseWithApply(t.Context(), dag, changeset, changesetDispositionApply, &preview,
				func(ctx context.Context, got *dagger.Changeset) error {
					calls++
					require.Same(t, changeset, got, "retain the exact export object")
					require.NoError(t, ctx.Err())
					return nil
				})
			require.NoError(t, err)
			require.Equal(t, test.applied, applied)
			if test.applied {
				require.Equal(t, 1, calls)
			} else {
				require.Zero(t, calls)
			}
			require.Empty(t, preview.String(), "explicit apply never displayed this preview")
			requireChangesetApplyPathQuery(t, conn)
		})
	}
}

func TestChangesetPreviewDispositionsUnchanged(t *testing.T) {
	previousFrontend := Frontend
	t.Cleanup(func() { Frontend = previousFrontend })
	frontend := &idtui.FrontendMock{
		HandleFormFunc: func(context.Context, *huh.Form) error { return idtui.ErrNonInteractive },
	}
	Frontend = frontend
	for _, test := range []struct {
		name        string
		disposition changesetDisposition
		preview     string
		noOutput    bool
		wantErr     string
		wantPrompt  bool
	}{
		{"no apply preview", changesetDispositionNoApply, changesetApplyTestDiffResponse, false, "", false},
		{"no apply missing writer", changesetDispositionNoApply, changesetApplyTestDiffResponse, true, "no preview output configured", false},
		{"prompt", changesetDispositionPrompt, changesetApplyTestDiffResponse, false, "pass -y/--auto-apply", true},
		{"empty prompt", changesetDispositionPrompt, `{"data":{"changeset":{"diffStats":[]}}}`, false, "", false},
		{"empty no apply", changesetDispositionNoApply, `{"data":{"changeset":{"diffStats":[]}}}`, true, "", false},
		{"unknown disposition", changesetDisposition(99), changesetApplyTestDiffResponse, false, "unknown changeset disposition 99", false},
		{"empty unknown remains no-op", changesetDisposition(99), `{"data":{"changeset":{"diffStats":[]}}}`, false, "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			dag, conn := newChangesetApplyTestClient(t,
				changesetApplyTestReply{body: changesetApplyTestIDResponse},
				changesetApplyTestReply{body: test.preview},
			)
			var out bytes.Buffer
			var previewOut io.Writer = &out
			if test.noOutput {
				previewOut = nil
			}
			formCalls := len(frontend.HandleFormCalls())
			applied, err := handleChangesetResponseWithApply(t.Context(), dag, changesetApplyTestID, test.disposition, previewOut,
				func(context.Context, *dagger.Changeset) error {
					t.Fatal("must not apply in this fixture")
					return nil
				})
			require.False(t, applied)
			if test.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, test.wantErr)
			}
			if test.wantPrompt {
				require.Len(t, frontend.HandleFormCalls(), formCalls+1)
			} else {
				require.Len(t, frontend.HandleFormCalls(), formCalls)
			}
			require.Len(t, conn.requests, 2)
			require.Contains(t, conn.requests[1].Query, "diffStats")
			require.NotContains(t, conn.requests[1].Query, "addedPaths")
			if test.name == "no apply preview" {
				require.Contains(t, out.String(), "result.txt")
				require.Contains(t, out.String(), "Generated changes were not applied (--no-apply).")
			}
		})
	}
}
