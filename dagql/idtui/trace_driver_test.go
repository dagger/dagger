package idtui

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/dagger/dagger/dagql/dagui"
	"go.opentelemetry.io/otel/trace"
)

// TestDriveTrace is an operator console for the interactive trace TUI, not an
// assertion. It loads a trace, replays a script of keystrokes, and prints the
// rendered screen — so the frontend can be driven ad-hoc (by a human or an LLM)
// to explore a misbehaving trace without a pty, a CLI, or a live event loop.
// It's opt-in: with no source env set it skips.
//
// Source (one of); paths are relative to the package dir, go test's cwd:
//
//	DRIVE_FIXTURE=testdata/traces/x.json   # a recorded fixture (offline)
//	DRIVE_TRACE_ID=<traceID>               # fetch live from Cloud (needs
//	                                       # DAGGER_CLOUD_URL / auth)
//
// Driving:
//
//	DRIVE_KEYS="down down right +"   # keystroke script (see tuist.ParseKey);
//	                                 # commas ok; "down*3" repeats a key
//	DRIVE_ZOOM=<spanHex>             # jump to a span first (the --span link)
//	DRIVE_VERBOSITY=2                # FrontendOpts.Verbosity (default: 0)
//	DRIVE_WIDTH=120 DRIVE_HEIGHT=40  # terminal size
//	DRIVE_STEPS=1                    # print the screen after every key
//
// Example (drive a recorded fixture, human styling, expand the root):
//
//	cd cloud/dagger
//	env -u AI_AGENT -u CLAUDECODE \
//	    DRIVE_FIXTURE=testdata/traces/call_loadfail.json \
//	    DRIVE_KEYS="right" \
//	    go test ./dagql/idtui/ -run TestDriveTrace -count=1 -v
//
// Leave AI_AGENT/CLAUDECODE set for the compact ASCII (agent) styling instead.
func TestDriveTrace(t *testing.T) {
	fixturePath := os.Getenv("DRIVE_FIXTURE")
	traceID := os.Getenv("DRIVE_TRACE_ID")
	if fixturePath == "" && traceID == "" {
		t.Skip("set DRIVE_FIXTURE=<path> or DRIVE_TRACE_ID=<id> to drive a trace")
	}

	var fix *TraceFixture
	source := "fixture"
	switch {
	case fixturePath != "":
		fix = LoadTraceFixture(t, fixturePath)
	default:
		source = "live"
		fix = liveTraceFixture(t, traceID)
	}

	sess := newTraceSession(t, fix, func(opts *dagui.FrontendOpts) {
		if v := os.Getenv("DRIVE_VERBOSITY"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				t.Fatalf("DRIVE_VERBOSITY=%q: %v", v, err)
			}
			opts.Verbosity = n
		}
	})

	if w, h := os.Getenv("DRIVE_WIDTH"), os.Getenv("DRIVE_HEIGHT"); w != "" || h != "" {
		sess.Resize(atoiOr(t, w, 120), atoiOr(t, h, 40))
	}

	banner := fmt.Sprintf("== TRACE %s ==  source=%s  spans=%d  logs=%d",
		fix.TraceID, source, len(fix.Spans), len(fix.Logs))
	frame := sess.Render()

	if z := os.Getenv("DRIVE_ZOOM"); z != "" {
		sid, err := trace.SpanIDFromHex(z)
		if err != nil {
			t.Fatalf("DRIVE_ZOOM=%q: %v", z, err)
		}
		frame = sess.Zoom(dagui.SpanID{SpanID: sid})
	}

	keys := parseDriveKeys(os.Getenv("DRIVE_KEYS"))
	steps := os.Getenv("DRIVE_STEPS") != ""

	// Strip UI color/escape codes by default — a plain screen is what an LLM
	// operator wants. DRIVE_RAW=1 keeps the ANSI (e.g. to inspect styling).
	show := ansi.Strip
	if os.Getenv("DRIVE_RAW") != "" {
		show = func(s string) string { return s }
	}

	fmt.Printf("\n%s\n\n%s\n", banner, show(frame))

	for i, k := range keys {
		frame = sess.Press(k)
		if steps {
			fmt.Printf("\n-- after %q (%d/%d) --\n\n%s\n", k, i+1, len(keys), show(frame))
		}
	}
	if len(keys) > 0 && !steps {
		fmt.Printf("\n-- after keys: %s --\n\n%s\n", strings.Join(keys, " "), show(frame))
	}

	st := sess.Network()
	su, sl := st.op(opSpanUpdates), st.op(opSpanLogs)
	fmt.Printf("\n== network ==\n"+
		"GetSpanUpdates: %d reqs / %d records / %d bytes\n"+
		"GetSpanLogs:    %d reqs / %d records / %d bytes\n"+
		"log spans fetched: %d\n",
		su.Requests, su.Records, su.Bytes,
		sl.Requests, sl.Records, sl.Bytes,
		len(st.logRequests))
}

// parseDriveKeys splits a key script into individual key specs. Tokens are
// whitespace- or comma-separated; a "key*N" token repeats that key N times.
func parseDriveKeys(script string) []string {
	fields := strings.FieldsFunc(script, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == ','
	})
	var keys []string
	for _, f := range fields {
		key, count := f, 1
		if star := strings.LastIndex(f, "*"); star > 0 {
			if n, err := strconv.Atoi(f[star+1:]); err == nil && n > 0 {
				key, count = f[:star], n
			}
		}
		for range count {
			keys = append(keys, key)
		}
	}
	return keys
}

func atoiOr(t *testing.T, s string, fallback int) int {
	t.Helper()
	if s == "" {
		return fallback
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatalf("expected an integer, got %q: %v", s, err)
	}
	return n
}
