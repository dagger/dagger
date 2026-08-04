package core

import (
	"strings"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/dagql/dagui"
)

func traceTargetSpanID(id byte) dagui.SpanID {
	return dagui.SpanID{SpanID: trace.SpanID{id}}
}

// traceTargetDB builds a small trace directly, the way dagql/idtui's report
// tests do -- no engine required, since resolution only reads a dagui.DB.
//
// It contains one root, one check name carried by two spans (an earlier and a
// later attempt) and one test case name carried by two spans, which is exactly
// the ambiguity most-recent-wins resolves.
func traceTargetDB(t *testing.T) *dagui.DB {
	t.Helper()
	const (
		rootID byte = iota + 1
		checkOldID
		checkNewID
		otherCheckID
		testOldID
		testNewID
	)
	start := time.Unix(100, 0)
	snap := func(id byte, name string, parent dagui.SpanID, at time.Time) dagui.SpanSnapshot {
		return dagui.SpanSnapshot{
			ID:        traceTargetSpanID(id),
			TraceID:   dagui.TraceID{TraceID: trace.TraceID{1}},
			Name:      name,
			StartTime: at,
			EndTime:   at.Add(time.Second),
			ParentID:  parent,
			Status:    sdktrace.Status{},
			Final:     true,
		}
	}
	root := traceTargetSpanID(rootID)

	checkOld := snap(checkOldID, "lint:check", root, start)
	checkOld.CheckName = "lint:check"
	checkNew := snap(checkNewID, "lint:check", root, start.Add(10*time.Second))
	checkNew.CheckName = "lint:check"
	otherCheck := snap(otherCheckID, "unit:check", root, start)
	otherCheck.CheckName = "unit:check"

	testOld := snap(testOldID, "TestFoo", root, start)
	testOld.TestCaseName = "TestFoo"
	testOld.TestStatus = dagui.TestStatusSuccess
	testNew := snap(testNewID, "TestFoo", root, start.Add(10*time.Second))
	testNew.TestCaseName = "TestFoo"
	testNew.TestStatus = dagui.TestStatusSuccess

	db := dagui.NewDB()
	db.ImportSnapshots([]dagui.SpanSnapshot{
		snap(rootID, "dagger check", dagui.SpanID{}, start),
		checkOld, checkNew, otherCheck, testOld, testNew,
	})
	return db
}

// TestResolveTraceTargetChecksMostRecentWins: a name is how a reader refers to
// something it just saw run, so a retried check resolves to the latest attempt.
func TestResolveTraceTargetChecksMostRecentWins(t *testing.T) {
	db := traceTargetDB(t)
	got, err := resolveTraceTargetIn(db, traceTarget{Check: "lint:check"})
	if err != nil {
		t.Fatalf("resolve check: %v", err)
	}
	want := traceTargetSpanID(3).SpanID.String() // checkNewID
	if got != want {
		t.Fatalf("resolved check to %q, want the most recent span %q", got, want)
	}
}

func TestResolveTraceTargetTestsMostRecentWins(t *testing.T) {
	db := traceTargetDB(t)
	got, err := resolveTraceTargetIn(db, traceTarget{Test: "TestFoo"})
	if err != nil {
		t.Fatalf("resolve test: %v", err)
	}
	want := traceTargetSpanID(6).SpanID.String() // testNewID
	if got != want {
		t.Fatalf("resolved test to %q, want the most recent span %q", got, want)
	}
}

// TestResolveTraceTargetUnknownNames covers the error text: an agent that
// guessed a name must learn both that it was wrong and what it could have said.
func TestResolveTraceTargetUnknownNames(t *testing.T) {
	db := traceTargetDB(t)

	_, err := resolveTraceTargetIn(db, traceTarget{Check: "nope:check"})
	if err == nil {
		t.Fatal("expected an error for an unknown check name")
	}
	for _, want := range []string{`no check named "nope:check"`, "available checks", "lint:check", "unit:check"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("check error %q missing %q", err, want)
		}
	}

	_, err = resolveTraceTargetIn(db, traceTarget{Test: "TestNope"})
	if err == nil {
		t.Fatal("expected an error for an unknown test name")
	}
	for _, want := range []string{`no test case or suite named "TestNope"`, "available tests", "TestFoo"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("test error %q missing %q", err, want)
		}
	}
}

func TestResolveTraceTargetSpanErrors(t *testing.T) {
	db := traceTargetDB(t)

	if _, err := resolveTraceTargetIn(db, traceTarget{}); err == nil {
		t.Fatal("expected an error with no target")
	} else {
		for _, want := range []string{"span", "check", "test"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("empty-target error %q does not mention %q", err, want)
			}
		}
	}

	// Well-formed but absent from this trace.
	missing := dagui.SpanID{SpanID: trace.SpanID{0xaa}}.SpanID.String()
	if _, err := resolveTraceTargetIn(db, traceTarget{Span: missing}); err == nil ||
		!strings.Contains(err.Error(), "no span") {
		t.Fatalf("expected a not-found error for span %s, got %v", missing, err)
	}

	if _, err := resolveTraceTargetIn(db, traceTarget{Span: "not-hex"}); err == nil ||
		!strings.Contains(err.Error(), "invalid span ID") {
		t.Fatalf("expected an invalid-span error, got %v", err)
	}

	// A span that is present resolves to itself.
	present := traceTargetSpanID(2).SpanID.String()
	if got, err := resolveTraceTargetIn(db, traceTarget{Span: present}); err != nil || got != present {
		t.Fatalf("resolveTraceTargetIn(span: %s) = %q, %v", present, got, err)
	}
}

// TestReadTraceRerunSuggestion asserts the vocabulary swap: the LLM render path
// suggests a ReadTrace call, never a `dagger check` command.
func TestReadTraceRerunSuggestion(t *testing.T) {
	heading, body := readTraceRerunSuggestion([]string{"ci:bootstrap", "go:lint"})
	if heading == "" || heading == "RUN LOCALLY" {
		t.Fatalf("expected a heading fitting the ReadTrace content, got %q", heading)
	}
	joined := strings.Join(body, "\n")
	for _, want := range []string{`ReadTrace(check: "ci:bootstrap")`, `ReadTrace(check: "go:lint")`} {
		if !strings.Contains(joined, want) {
			t.Errorf("suggestion body missing %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "dagger check") {
		t.Errorf("suggestion body still points at the CLI:\n%s", joined)
	}
}
