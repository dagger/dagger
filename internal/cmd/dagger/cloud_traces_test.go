package daggercmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	enginetel "github.com/dagger/dagger/engine/telemetry"
	cloudapi "github.com/dagger/dagger/internal/cloud"
)

func TestWriteCloudTracesStatus(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status enginetel.CloudEmitStatus
		want   string
	}{
		{
			name:   "login with org",
			status: enginetel.CloudEmitStatus{Emitting: true, Credential: "dagger login", Org: "acme"},
			want:   "emitting\n  credential: dagger login (org: acme)\n",
		},
		{
			name:   "login without org",
			status: enginetel.CloudEmitStatus{Credential: "dagger login"},
			want:   "not emitting\n  reason: logged in, but no org is selected\n  fix:    dagger cloud org use <org>\n",
		},
		{
			name:   "invalid Cloud URL",
			status: enginetel.CloudEmitStatus{Credential: "dagger login", Org: "acme", Err: enginetel.ErrInvalidCloudURL},
			want:   "not emitting\n  reason: DAGGER_CLOUD_URL is not a valid URL\n  fix:    unset DAGGER_CLOUD_URL, or set it to a valid URL\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			require.NoError(t, writeCloudTracesStatus(&out, tc.status))
			require.Equal(t, tc.want, out.String())
		})
	}
}

func TestParseTimeFlag(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

	got, err := parseTimeFlag("since", "", now)
	require.NoError(t, err)
	require.Nil(t, got)

	for value, want := range map[string]time.Time{
		"30m":                  now.Add(-30 * time.Minute),
		"2d":                   now.Add(-48 * time.Hour),
		"1w":                   now.Add(-7 * 24 * time.Hour),
		"1.5d":                 now.Add(-36 * time.Hour),
		"2026-09-01T10:00:00Z": time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC),
	} {
		got, err := parseTimeFlag("since", value, now)
		require.NoError(t, err, value)
		require.True(t, want.Equal(*got), "%s: want %s, got %s", value, want, got)
	}

	got, err = parseTimeFlag("since", "2026-09-01", now)
	require.NoError(t, err)
	require.Equal(t, "2026-09-01", got.Format("2006-01-02"))

	for _, bad := range []string{"yesterday", "-2d", "-5m", "d"} {
		_, err := parseTimeFlag("since", bad, now)
		require.ErrorContains(t, err, "--since", bad)
	}
}

func TestCloudTracesListFilter(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	o := &cloudTracesListOptions{
		repos:       []string{"acme/app"},
		status:      "failed",
		ci:          true,
		command:     "dagger check*",
		since:       "1d",
		minDuration: 90 * time.Second,
	}
	f, err := o.filter(now)
	require.NoError(t, err)
	require.Equal(t, []string{"acme/app"}, f.Repos)
	require.Equal(t, "FAILED", f.Status)
	require.NotNil(t, f.Local)
	require.False(t, *f.Local, "--ci")
	require.Equal(t, "dagger check*", f.Name)
	require.True(t, now.Add(-24*time.Hour).Equal(*f.Since))
	require.Nil(t, f.Until)
	require.Equal(t, 90.0, f.MinDuration)

	_, err = (&cloudTracesListOptions{status: "broken"}).filter(now)
	require.ErrorContains(t, err, "--status")
}

func testTraceSummaries(now time.Time) []cloudapi.TraceSummary {
	end := now.Add(-time.Minute)
	branch := "main"
	provider := "github"
	return []cloudapi.TraceSummary{
		{
			ID:        "2f123ba77bf7bd2d4db2f70ed20613e8",
			Name:      "dagger check",
			Status:    &cloudapi.TraceStatus{Code: "STATUS_CODE_ERROR", Message: "exit 1"},
			Timestamp: end.Add(-2 * time.Minute),
			EndTime:   &end,
			Sender:    &cloudapi.TraceSender{Name: "Ada"},
			TraceMetadata: cloudapi.TraceMetadata{
				Git: &cloudapi.TraceGitMetadata{Remote: "github.com/acme/app", Ref: "0123456789abcdef", Branch: &branch},
				CI: &cloudapi.TraceCIMetadata{
					Provider: &provider,
					Change:   &cloudapi.TraceCIChange{ID: "42", Title: "Fix it", HeadSHA: "fedcba9876543210"},
				},
			},
		},
		{
			ID:        "0102030405060708090a0b0c0d0e0f10",
			Name:      "dagger call build",
			Status:    &cloudapi.TraceStatus{Code: "STATUS_CODE_UNSET"},
			Timestamp: now.Add(-30 * time.Second),
			Local:     true,
			TraceMetadata: cloudapi.TraceMetadata{
				Git: &cloudapi.TraceGitMetadata{Ref: "0123456789abcdef"},
			},
		},
	}
}

func testTraceRows(now time.Time) []traceRow {
	traces := testTraceSummaries(now)
	rows := make([]traceRow, len(traces))
	for i := range traces {
		rows[i] = newTraceRow("acme", &traces[i], now)
	}
	return rows
}

func TestNewTraceRow(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	rows := testTraceRows(now)

	ci := rows[0]
	require.Equal(t, "https://dagger.cloud/acme/traces/2f123ba77bf7bd2d4db2f70ed20613e8", ci.URL)
	require.Equal(t, cloudapi.TraceStateFailed, ci.Status)
	require.Equal(t, "42", ci.PR)
	require.Equal(t, "main", ci.Branch)
	require.Equal(t, "fedcba9876543210", ci.Commit, "the CI change's head wins over the git ref")
	require.Equal(t, "Fix it", ci.Title, "the CI change's title wins over the commit title")
	require.Equal(t, 120.0, ci.Duration)

	local := rows[1]
	require.Equal(t, cloudapi.TraceStateRunning, local.Status)
	require.Nil(t, local.EndedAt)
	require.Equal(t, 30.0, local.Duration, "a running trace runs until now")
}

func TestWriteTracesTable(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	var out bytes.Buffer
	require.NoError(t, writeTracesTable(&out, testTraceRows(now)))
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	require.Len(t, lines, 3)
	require.Equal(t, []string{"ID", "STATUS", "DURATION", "COMMAND", "BRANCH/PR", "SENDER", "STARTED"}, strings.Fields(lines[0]))
	require.Regexp(t, `^2f123ba77bf7bd2d4db2f70ed20613e8\s+failed\s+2m0s\s+dagger check\s+#42\s+Ada\s+`, lines[1])
	require.Regexp(t, `^0102030405060708090a0b0c0d0e0f10\s+running\s+30\.0s\s+dagger call build\s+0123456789ab\s+-\s+`, lines[2])
}

func TestRunTraceViewValidatesFlags(t *testing.T) {
	const id = "2f123ba77bf7bd2d4db2f70ed20613e8"
	cmd := &cobra.Command{}
	for _, tc := range []struct {
		name string
		args []string
		o    traceViewOptions
		err  string
	}{
		{"no trace", nil, traceViewOptions{}, "give a trace ID or URL, or use --last"},
		{"trace and --last", []string{id}, traceViewOptions{last: true}, "not both"},
		{"--output without --log", []string{id}, traceViewOptions{output: "f"}, "need --log"},
		{"--descendants without --span", []string{id}, traceViewOptions{log: true, sel: spanSelector{descendants: true}}, "--descendants needs --span"},
		{"two selectors", []string{id}, traceViewOptions{sel: spanSelector{check: "a", test: "b"}}, "mutually exclusive"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := tc.o
			require.ErrorContains(t, runTraceView(cmd, tc.args, &o), tc.err)
		})
	}
}

func TestTraceWebOrg(t *testing.T) {
	prev := cloudOrgFlag
	t.Cleanup(func() { cloudOrgFlag = prev })
	t.Setenv("DAGGER_CLOUD_TOKEN", "dag_local_secret")

	cloudOrgFlag = ""
	org, err := traceWebOrg("")
	require.NoError(t, err)
	require.Equal(t, "local", org, "the credential's org is the last choice")

	org, err = traceWebOrg("fromurl")
	require.NoError(t, err)
	require.Equal(t, "fromurl", org, "the trace URL's org wins over the credential's org")

	cloudOrgFlag = "flag"
	org, err = traceWebOrg("fromurl")
	require.NoError(t, err)
	require.Equal(t, "flag", org, "--org wins")
}

func TestResolveTraceViewArgLast(t *testing.T) {
	var lastOrg any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(req.Query, "lastUserTrace"):
			lastOrg = req.Variables["org"]
			_, _ = w.Write([]byte(`{"data":{"org":{"lastUserTrace":{"id":"2f123ba77bf7bd2d4db2f70ed20613e8"}}}}`))
		case strings.Contains(req.Query, "org(name"):
			_, _ = w.Write([]byte(`{"data":{"org":{"id":"org-id","name":"acme"}}}`))
		default: // the user query: a token names no user
			_, _ = w.Write([]byte(`{"errors":[{"message":"not a user"}]}`))
		}
	}))
	defer srv.Close()
	t.Setenv("DAGGER_CLOUD_URL", srv.URL)
	t.Setenv("DAGGER_CLOUD_TOKEN", "dag_acme_secret")
	prev := cloudOrgFlag
	t.Cleanup(func() { cloudOrgFlag = prev })
	cloudOrgFlag = ""

	ref, err := resolveTraceViewArg(t.Context(), nil, true)
	require.NoError(t, err)
	require.Equal(t, cloudapi.TraceRef{TraceID: "2f123ba77bf7bd2d4db2f70ed20613e8", Org: "acme"}, ref)
	require.Equal(t, "acme", lastOrg)
}
