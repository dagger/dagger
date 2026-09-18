package dagui

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func lookupSnapshot(id byte, name string, failed bool) SpanSnapshot {
	snap := testSnapshot(id, name, SpanID{}, TestStatusSuccess)
	snap.StartTime = time.Unix(int64(id), 0)
	snap.EndTime = snap.StartTime.Add(time.Second)
	if failed {
		snap.Status = sdktrace.Status{Code: codes.Error}
	}
	return snap
}

func TestFindCheckSpanPrefersFailed(t *testing.T) {
	db := NewDB()
	passing := lookupSnapshot(1, "lint (retry)", false)
	passing.CheckName = "lint"
	failing := lookupSnapshot(2, "lint", true)
	failing.CheckName = "lint"
	other := lookupSnapshot(3, "build", false)
	other.CheckName = "build"
	db.ImportSnapshots([]SpanSnapshot{passing, failing, other})

	require.Equal(t, failing.ID, db.FindCheckSpan("lint").ID, "a failed check must win over a passing one of the same name")
	require.Equal(t, other.ID, db.FindCheckSpan("build").ID)
	require.Nil(t, db.FindCheckSpan("nope"))
}

func TestFindTestSpanMatchesNameForms(t *testing.T) {
	db := NewDB()
	inSuite := lookupSnapshot(1, "TestFoo", false)
	inSuite.TestSuiteName = "pkg"
	spanNamed := lookupSnapshot(2, "pkg/TestBar", false)
	spanNamed.TestCaseName = "TestBar"
	failingRetry := lookupSnapshot(3, "TestFoo", true)
	failingRetry.TestSuiteName = "pkg"
	notATest := lookupSnapshot(4, "TestBaz", false)
	notATest.TestCaseName = ""
	db.ImportSnapshots([]SpanSnapshot{inSuite, spanNamed, failingRetry, notATest})

	require.Equal(t, failingRetry.ID, db.FindTestSpan("TestFoo").ID, "the failing case wins")
	require.Equal(t, failingRetry.ID, db.FindTestSpan("pkg TestFoo").ID, "matches the <suite> <case> form")
	require.Equal(t, spanNamed.ID, db.FindTestSpan("pkg/TestBar").ID, "matches the span name")
	require.Nil(t, db.FindTestSpan("TestBaz"), "a span without a test case name is not a test")
}
