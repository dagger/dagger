package idtui

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/dagui"
)

func TestConsoleSpanDetail(t *testing.T) {
	db := dagui.NewDB()
	rootID := prettyTestSpanID(1)
	midID := prettyTestSpanID(2)
	leafID := prettyTestSpanID(3)
	start := time.Unix(100, 0).UTC()
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{
			ID:        rootID,
			TraceID:   prettyTestTraceID(),
			Name:      "root call",
			StartTime: start,
			EndTime:   start.Add(3 * time.Second),
			Final:     true,
		},
		{
			ID:          midID,
			TraceID:     prettyTestTraceID(),
			Name:        "middle span",
			StartTime:   start.Add(time.Second),
			EndTime:     start.Add(3 * time.Second),
			ParentID:    rootID,
			Passthrough: true,
			RollUpLogs:  true,
			Final:       true,
		},
		{
			ID:           leafID,
			TraceID:      prettyTestTraceID(),
			Name:         "leaf op",
			StartTime:    start.Add(2 * time.Second),
			EndTime:      start.Add(3 * time.Second),
			ParentID:     midID,
			Internal:     true,
			Encapsulated: true,
			Cached:       true,
			Final:        true,
		},
	})
	db.SetPrimarySpan(rootID)
	fe := NewWithDB(io.Discard, db)

	detail, ok := fe.consoleSpanDetail(leafID)
	if !ok {
		t.Fatalf("consoleSpanDetail(%s) = not found", leafID)
	}
	for _, want := range []string{
		"span:     " + leafID.String() + "  leaf op",
		"status:   ok",
		"started:  " + start.Add(2*time.Second).Format(time.RFC3339Nano),
		"ended:    " + start.Add(3*time.Second).Format(time.RFC3339Nano),
		"duration: 1s",
		"flags:    internal encapsulated cached",
		"parents (nearest first):",
		"  " + midID.String() + "  middle span  [passthrough rollUpLogs]",
		"  " + rootID.String() + "  root call",
	} {
		if !strings.Contains(detail, want) {
			t.Errorf("span detail missing %q:\n%s", want, detail)
		}
	}
	// The flagless root ancestor must not grow an empty flag bracket.
	if strings.Contains(detail, "root call  [") {
		t.Errorf("root ancestor line should have no flag bracket:\n%s", detail)
	}

	// A root span reports its (lack of a) parent chain explicitly.
	rootDetail, ok := fe.consoleSpanDetail(rootID)
	if !ok {
		t.Fatalf("consoleSpanDetail(%s) = not found", rootID)
	}
	if !strings.Contains(rootDetail, "(none — root span)") {
		t.Errorf("root detail missing empty-parent marker:\n%s", rootDetail)
	}
	if !strings.Contains(rootDetail, "flags:    (none)") {
		t.Errorf("root detail missing empty flags marker:\n%s", rootDetail)
	}

	// Unknown spans are reported as such, not as an empty page.
	if _, ok := fe.consoleSpanDetail(prettyTestSpanID(99)); ok {
		t.Error("consoleSpanDetail of unknown span reported ok")
	}
}
