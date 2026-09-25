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
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			require.NoError(t, writeCloudTracesStatus(&out, tc.status))
			require.Equal(t, tc.want, out.String())
		})
	}
}

func TestParseTraceRef(t *testing.T) {
	const id = "2f123ba77bf7bd2d4db2f70ed20613e8"

	traceID, org, span, err := parseTraceRef(" " + strings.ToUpper(id) + "\n")
	require.NoError(t, err)
	require.Equal(t, id, traceID)
	require.Empty(t, org)
	require.Empty(t, span)

	traceID, org, span, err = parseTraceRef("https://dagger.cloud/acme/traces/" + id + "?span=0102030405060708")
	require.NoError(t, err)
	require.Equal(t, id, traceID)
	require.Equal(t, "acme", org)
	require.Equal(t, "0102030405060708", span)

	for _, bad := range []string{"", "nope", "https://dagger.cloud/acme/checks", "00000000000000000000000000000000"} {
		_, _, _, err := parseTraceRef(bad)
		require.Error(t, err, bad)
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
	prev := cloudTracesList
	t.Cleanup(func() { cloudTracesList = prev })
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

	cloudTracesList = prev
	cloudTracesList.repos = []string{"acme/app"}
	cloudTracesList.pr = "#42"
	cloudTracesList.status = "failed"
	cloudTracesList.ci = true
	cloudTracesList.command = "dagger check*"
	cloudTracesList.since = "1d"
	cloudTracesList.minDuration = 90 * time.Second
	f, err := cloudTracesListFilter(now)
	require.NoError(t, err)
	require.Equal(t, []string{"acme/app"}, f.Repos)
	require.Equal(t, "42", f.Change)
	require.Equal(t, "FAILED", f.Status)
	require.NotNil(t, f.Local)
	require.False(t, *f.Local)
	require.Equal(t, "dagger check*", f.Name)
	require.True(t, now.Add(-24*time.Hour).Equal(*f.Since))
	require.Nil(t, f.Until)
	require.Equal(t, 90.0, f.MinDuration)

	cloudTracesList = prev
	cloudTracesList.status = "broken"
	_, err = cloudTracesListFilter(now)
	require.ErrorContains(t, err, "--status")

	cloudTracesList = prev
	cloudTracesList.commit = "abc"
	_, err = cloudTracesListFilter(now)
	require.ErrorContains(t, err, "--commit")
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
			Sender:    &cloudapi.TraceSender{ID: "u1", Name: "Ada"},
			Git:       &cloudapi.TraceGit{Remote: "github.com/acme/app", Ref: "0123456789abcdef", Branch: &branch},
			CI: &cloudapi.TraceCI{
				Provider: &provider,
				Change:   &cloudapi.TraceCIChange{ID: "42", Title: "Fix it", HeadSHA: "fedcba9876543210"},
			},
		},
		{
			ID:        "0102030405060708090a0b0c0d0e0f10",
			Name:      "dagger call build",
			Status:    &cloudapi.TraceStatus{Code: "STATUS_CODE_UNSET"},
			Timestamp: now.Add(-30 * time.Second),
			Local:     true,
			Git:       &cloudapi.TraceGit{Ref: "0123456789abcdef"},
		},
	}
}

func TestWriteTracesTable(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	var out bytes.Buffer
	require.NoError(t, writeTracesTable(&out, testTraceSummaries(now), now))
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	require.Len(t, lines, 3)
	require.Equal(t, []string{"ID", "STATUS", "DURATION", "COMMAND", "BRANCH/PR", "SENDER", "STARTED"}, strings.Fields(lines[0]))
	require.Regexp(t, `^2f123ba77bf7bd2d4db2f70ed20613e8\s+failed\s+2m0s\s+dagger check\s+#42\s+Ada\s+`, lines[1])
	require.Regexp(t, `^0102030405060708090a0b0c0d0e0f10\s+running\s+30\.0s\s+dagger call build\s+0123456789ab\s+-\s+`, lines[2])
}

func TestWriteTracesJSON(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	var out bytes.Buffer
	require.NoError(t, writeTracesJSON(&out, "acme", testTraceSummaries(now), []string{"id", "url", "status", "pr", "branch", "duration"}, now))
	var rows []map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &rows))
	require.Equal(t, []map[string]any{
		{
			"id":       "2f123ba77bf7bd2d4db2f70ed20613e8",
			"url":      "https://dagger.cloud/acme/traces/2f123ba77bf7bd2d4db2f70ed20613e8",
			"status":   "failed",
			"pr":       "42",
			"branch":   "main",
			"duration": 120.0,
		},
		{
			"id":       "0102030405060708090a0b0c0d0e0f10",
			"url":      "https://dagger.cloud/acme/traces/0102030405060708090a0b0c0d0e0f10",
			"status":   "running",
			"pr":       "",
			"branch":   "",
			"duration": 30.0,
		},
	}, rows)
}

func TestRunTraceViewValidatesFlags(t *testing.T) {
	cmd := &cobra.Command{}
	for _, tc := range []struct {
		name string
		args []string
		o    traceViewOptions
		err  string
	}{
		{"no trace", nil, traceViewOptions{}, "give a trace ID or URL, or use --last"},
		{"trace and --last", []string{"2f123ba77bf7bd2d4db2f70ed20613e8"}, traceViewOptions{last: true}, "not both"},
		{"--output without --log", []string{"x"}, traceViewOptions{output: "f"}, "need --log"},
		{"--descendants without --span", []string{"x"}, traceViewOptions{log: true, descendants: true}, "--descendants needs --span"},
		{"two selectors", []string{"x"}, traceViewOptions{check: "a", test: "b"}, "mutually exclusive"},
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

	traceID, org, span, err := resolveTraceViewArg(t.Context(), nil, &traceViewOptions{last: true})
	require.NoError(t, err)
	require.Equal(t, "2f123ba77bf7bd2d4db2f70ed20613e8", traceID)
	require.Equal(t, "acme", org)
	require.Empty(t, span)
	require.Equal(t, "acme", lastOrg)
}
