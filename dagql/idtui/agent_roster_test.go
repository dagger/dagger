package idtui

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/muesli/termenv"
	"github.com/vito/tuist"
)

func renderRoster(t *testing.T, width int, entries []AgentRosterEntry) string {
	t.Helper()
	roster := NewAgentRoster(termenv.Ascii, func() []AgentRosterEntry {
		return entries
	})
	term := tuist.NewHeadlessTerminal(width, 1)
	tui := tuist.New(term)
	tui.AddChild(roster)
	tui.RenderOnce()
	return strings.Join(tui.Frame(), "\n")
}

// TestAgentRosterHiddenBelowTwoAgents locks in the rule that keeps the
// ordinary single-agent session exactly as it was: a roster of one is pure
// chrome, since the status line already reports whether that agent is busy.
func TestAgentRosterHiddenBelowTwoAgents(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entries []AgentRosterEntry
	}{
		{"no agents", nil},
		{"one agent", []AgentRosterEntry{{Name: "interactive", State: "RUNNING"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			roster := NewAgentRoster(termenv.Ascii, func() []AgentRosterEntry {
				return tc.entries
			})
			if roster.Visible() {
				t.Fatal("roster should be hidden")
			}
			if got := roster.Height(); got != 0 {
				t.Fatalf("hidden roster must reserve no height, got %d", got)
			}
			if line := strings.TrimSpace(renderRoster(t, 80, tc.entries)); line != "" {
				t.Fatalf("hidden roster rendered %q", line)
			}
		})
	}
}

// TestAgentRosterRendersEveryAgent covers the strip's whole job: every agent
// present, each with a jump number and a state flag, on one line.
func TestAgentRosterRendersEveryAgent(t *testing.T) {
	line := renderRoster(t, 100, []AgentRosterEntry{
		{Name: "chief", State: "RUNNING"},
		{Name: "scout", State: "IDLE"},
		{Name: "docs", State: "PAUSED"},
		{Name: "tests", State: "WAITING_INPUT", WaitingOn: "ok to delete testdata/legacy?"},
		{Name: "bench", State: "FAILED"},
	})

	for _, want := range []string{
		"1:chief ●run",
		"2:scout ○idle",
		"3:docs ‖paused",
		"4:tests !needs you",
		"5:bench ✘failed",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("expected %q in roster, got:\n%q", want, line)
		}
	}
	if got := strings.Count(line, "\n"); got != 0 {
		t.Fatalf("roster must stay one line, got %d newlines:\n%q", got, line)
	}
}

// TestAgentRosterMarksFocusAndReachability keeps the two switcher facts
// legible without color: which agent the prompt addresses (tmux's
// current-window mark), and which agents can be addressed at all — an entry
// whose handle the client could not rebuild is watch-only, and must not look
// like one you can talk to.
func TestAgentRosterMarksFocusAndReachability(t *testing.T) {
	line := renderRoster(t, 100, []AgentRosterEntry{
		{ID: "a", Name: "chief", State: "IDLE"},
		{ID: "b", Name: "scout", State: "RUNNING", Focused: true},
		{ID: "c", Name: "ghost", State: "RUNNING", ReadOnly: true},
	})

	if !strings.Contains(line, "2:scout*") {
		t.Fatalf("expected the focused agent to be marked, got:\n%q", line)
	}
	if strings.Contains(line, "1:chief*") {
		t.Fatalf("only the focused agent may be marked, got:\n%q", line)
	}
	if !strings.Contains(line, "3:ghost·") {
		t.Fatalf("expected the unaddressable agent to be marked, got:\n%q", line)
	}
}

// TestAgentRosterNumbersOnlyJumpableEntries: the numbers are jump targets
// (alt+1…9 from the prompt, 1…9 in nav mode), so an entry past the ninth gets
// none rather than advertising a key that does nothing.
func TestAgentRosterNumbersOnlyJumpableEntries(t *testing.T) {
	entries := make([]AgentRosterEntry, 0, 10)
	for _, name := range []string{
		"a1", "a2", "a3", "a4", "a5", "a6", "a7", "a8", "a9", "a10",
	} {
		entries = append(entries, AgentRosterEntry{Name: name, State: "IDLE"})
	}
	line := renderRoster(t, 400, entries)
	if !strings.Contains(line, "9:a9") {
		t.Fatalf("expected the ninth agent to be numbered, got:\n%q", line)
	}
	if strings.Contains(line, "10:a10") || !strings.Contains(line, "a10 ") {
		t.Fatalf("the tenth agent must be listed without a jump number, got:\n%q", line)
	}
}

// TestAgentRosterUnknownStateIsQuiet covers the window between an agent's loop
// span appearing and its first state record arriving: the agent is known to
// exist but its state is not, and the strip must not invent one.
func TestAgentRosterUnknownStateIsQuiet(t *testing.T) {
	line := renderRoster(t, 80, []AgentRosterEntry{
		{Name: "chief", State: "RUNNING"},
		{Name: "fresh"},
	})
	if !strings.Contains(line, "fresh") {
		t.Fatalf("expected the stateless agent to still be listed, got:\n%q", line)
	}
	for _, unwanted := range []string{"idle", "run", "failed", "needs you"} {
		if strings.Contains(line, "fresh "+unwanted) {
			t.Fatalf("stateless agent must not be given state %q, got:\n%q", unwanted, line)
		}
	}
}

// TestAgentRosterTruncatesToWidth guards the height accounting: the strip is
// budgeted as exactly one line, so a roster too wide for the terminal must
// truncate rather than wrap.
func TestAgentRosterTruncatesToWidth(t *testing.T) {
	entries := make([]AgentRosterEntry, 0, 12)
	for _, name := range []string{
		"chief", "scout", "docs", "tests", "bench", "lint",
		"fmt", "vet", "build", "release", "triage", "review",
	} {
		entries = append(entries, AgentRosterEntry{Name: name, State: "RUNNING"})
	}

	const width = 40
	line := renderRoster(t, width, entries)
	if got := strings.Count(line, "\n"); got != 0 {
		t.Fatalf("roster wrapped instead of truncating: %d newlines\n%q", got, line)
	}
	if len([]rune(strings.TrimRight(line, " "))) > width {
		t.Fatalf("roster exceeded terminal width %d:\n%q", width, line)
	}
	if !strings.Contains(line, "…") {
		t.Fatalf("expected an ellipsis marking the truncation, got:\n%q", line)
	}
}

// TestAgentRosterEntriesFromDB closes the seam between the trace DB and the
// strip: the frontend must source its entries from the agents the engine
// published, including a worker whose loop span sits under a Boundary (its
// chief's tool call) — the containment that hides a fixture service must not
// hide an agent.
func TestAgentRosterEntriesFromDB(t *testing.T) {
	db := dagui.NewDB()
	start := time.Unix(100, 0)
	traceID := prettyTestTraceID()
	rootID := prettyTestSpanID(1)
	chiefID := prettyTestSpanID(2)
	toolID := prettyTestSpanID(3)
	workerID := prettyTestSpanID(4)

	db.ImportSnapshots([]dagui.SpanSnapshot{
		{
			ID:        rootID,
			TraceID:   traceID,
			Name:      "dagger",
			StartTime: start,
		},
		{
			ID:        chiefID,
			TraceID:   traceID,
			ParentID:  rootID,
			Name:      "agent: interactive",
			StartTime: start.Add(time.Second),
			Agent:     true,
			AgentID:   "agent-chief",
			AgentName: "interactive",
		},
		{
			// The chief's spawn tool call: a Boundary, which is exactly the
			// containment SurfacedServices would hide behind.
			ID:        toolID,
			TraceID:   traceID,
			ParentID:  chiefID,
			Name:      `spawn(name: "scout")`,
			StartTime: start.Add(2 * time.Second),
			Boundary:  true,
		},
		{
			ID:        workerID,
			TraceID:   traceID,
			ParentID:  toolID,
			Name:      "agent: scout",
			StartTime: start.Add(3 * time.Second),
			Agent:     true,
			AgentID:   "agent-scout",
			AgentName: "scout",
		},
	})

	fe := NewWithDB(io.Discard, db)
	entries := fe.agentRosterEntries()
	if len(entries) != 2 {
		t.Fatalf("expected chief and worker, got %d: %+v", len(entries), entries)
	}
	if entries[0].Name != "interactive" || entries[1].Name != "scout" {
		t.Fatalf("unexpected roster: %+v", entries)
	}
}
