package daggercmd

import (
	"bytes"
	"testing"

	cloudapi "github.com/dagger/dagger/internal/cloud"
	"github.com/spf13/cobra"
)

func TestAnalyzeRender(t *testing.T) {
	// Render without fetching logs (nil client is safe in this mode).
	analyzeNoLogs = true
	analyzeLogLines = 0
	t.Cleanup(func() { analyzeNoLogs = false; analyzeLogLines = 20 })

	// Pin agent mode so the test is deterministic regardless of the ambient
	// environment: agents get plain output (no color) and ASCII status tokens.
	t.Setenv("AI_AGENT", "test")

	tq := &cloudapi.TraceQuestions{
		OverallStatus: &cloudapi.TraceOverallStatus{
			TraceID: "a0d14706", SpanID: "32370f63",
			Command: "test-split:test-container", Outcome: "failed",
			Error: "check failed due to an internal error: exit code: 1",
		},
		FailingCommands: []cloudapi.FailingCommand{
			{SpanID: "52311111", Command: "otelgotest -p 8 -timeout=15m ./...", Error: "exit code: 1"},
		},
		Checks: []cloudapi.TraceCheckStatus{
			{Name: "lint", SpanID: "aaaa", Status: "failed", Error: "lint failed"},
			{Name: "fmt", SpanID: "bbbb", Status: "passed"},
		},
		FailedTests: []cloudapi.FailedTest{
			{Name: "TestContainer", Suite: "core/integration", SpanID: "e4985ed2", FailureStatus: "fail (continuation)"},
			{Name: "TestContainer/TestSystemGoProxy", Suite: "core/integration", SpanID: "3ce9fe84",
				FailureStatus: "fail (continuation)", OriginCommand: "go test -c -o ./test ./core/integration", OriginError: "exit code: 1"},
		},
	}

	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cloudCLI.printAnalysis(cmd, nil, "0c8f0f6c", "a0d14706", tq)
	t.Logf("\n%s", buf.String())

	for _, want := range []string{
		"Status:  [FAILED]",
		"== ROOT CAUSE ==",
		"[root cause] otelgotest",
		"== CHECKS (1 passed, 1 failed, 2 total) ==",
		"[FAILED] lint",
		"[PASSED] fmt",
		"== FAILED TESTS (2) ==",
		"[FAILED] core/integration > TestContainer",
		"caused by: go test -c -o ./test ./core/integration",
		"== MORE CONTEXT ==",
		"Full call tree, arguments, and timing:  dagger trace --full a0d14706",
		"dagger cloud logs a0d14706 <span-id> -o span.log",
	} {
		if !bytes.Contains(buf.Bytes(), []byte(want)) {
			t.Errorf("output missing %q", want)
		}
	}
}
