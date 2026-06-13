package cloud

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCheckReportQuery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/query", r.URL.Path)
		var req graphqlRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.Equal(t, "GetCheckReport", req.OpName)
		require.Contains(t, req.Query, "report(input: $input)")
		require.Equal(t, "dagger", req.Variables["org"])
		require.Equal(t, "check-1", req.Variables["checkID"])

		input, ok := req.Variables["input"].(map[string]any)
		require.True(t, ok)
		require.EqualValues(t, 24, input["logLines"])
		require.EqualValues(t, 3, input["maxFailedSpans"])

		_, _ = w.Write([]byte(`{
			"data": {
				"org": {
					"check": {
						"report": {
							"checkId": "check-1",
							"checkName": "go:test",
							"status": "FAILURE",
							"summary": "go:test failed",
							"traceId": "trace",
							"spanId": "span",
							"traceUrl": "https://dagger.cloud/dagger/traces/trace",
							"generatedAt": "2026-06-05T00:00:00Z",
							"partial": false,
							"notices": [],
							"failure": {
								"message": "go:test failed",
								"roots": [{
									"spanId": "root",
									"traceId": "trace",
									"name": "go test",
									"message": "exit code 1",
									"logs": {
										"lines": ["FAIL"],
										"truncated": false,
										"totalLineCount": 1
									}
								}]
							},
							"tests": {
								"total": 2,
								"passed": 1,
								"failed": 1,
								"skipped": 0,
								"running": 0,
								"failures": [{
									"name": "TestFoo",
									"suite": "pkg",
									"status": "failure",
									"spanId": "test-span",
									"traceId": "trace",
									"message": "expected true"
								}],
								"skippedTests": []
							}
						}
					}
				}
			}
		}`))
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	client := &Client{u: u, h: srv.Client()}

	report, err := client.CheckReport(t.Context(), "dagger", "check-1", CheckReportOptions{
		LogLines:       24,
		MaxFailedSpans: 3,
	})
	require.NoError(t, err)
	require.Equal(t, "check-1", report.CheckID)
	require.Equal(t, "go:test failed", report.Summary)
	require.Len(t, report.Failure.Roots, 1)
	require.Equal(t, []string{"FAIL"}, report.Failure.Roots[0].Logs.Lines)
	require.NotNil(t, report.Failure.Roots[0].Logs.TotalLineCount)
	require.Equal(t, 1, *report.Failure.Roots[0].Logs.TotalLineCount)
	require.Equal(t, 2, report.Tests.Total)
	require.Len(t, report.Tests.Failures, 1)
}

func TestIsGraphQLFieldUnavailable(t *testing.T) {
	err := graphQLErrors{{
		Message: `Cannot query field "report" on type "Check".`,
	}}
	require.True(t, IsGraphQLFieldUnavailable(err, "report"))
	require.True(t, IsCheckReportUnavailable(err))
	require.False(t, IsGraphQLFieldUnavailable(err, "traceId"))
	require.False(t, IsGraphQLFieldUnavailable(errors.New("nope"), "report"))

	err = graphQLErrors{{
		Message: `Unknown type "CheckReportInput".`,
	}}
	require.True(t, IsCheckReportUnavailable(err))
}
