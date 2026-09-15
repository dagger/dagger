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
	return renderRosterWithProfile(t, width, entries, termenv.Ascii)
}

func renderRosterWithProfile(t *testing.T, width int, entries []AgentRosterEntry, profile termenv.Profile) string {
	t.Helper()
	roster := NewAgentRoster(profile, func() []AgentRosterEntry {
		return entries
	})
	term := tuist.NewHeadlessTerminal(width, 1)
	tui := tuist.New(term)
	tui.AddChild(roster)
	tui.RenderOnce()
	return strings.Join(tui.Frame(), "\n")
}

// TestAgentRosterAlwaysShowsPublishedAgents locks in the roster's dual role as
// switcher and state indicator. A single entry renders, but is not switchable.
func TestAgentRosterAlwaysShowsPublishedAgents(t *testing.T) {
	empty := NewAgentRoster(termenv.Ascii, func() []AgentRosterEntry { return nil })
	if empty.Visible() || empty.Switchable() || empty.Height() != 0 {
		t.Fatal("an empty roster should remain hidden")
	}

	entries := []AgentRosterEntry{{Name: "interactive", State: "RUNNING"}}
	roster := NewAgentRoster(termenv.Ascii, func() []AgentRosterEntry { return entries })
	if !roster.Visible() {
		t.Fatal("a single published agent should be visible")
	}
	if roster.Switchable() {
		t.Fatal("a single published agent should not enable focus shortcuts")
	}
	if got := roster.Height(); got != 1 {
		t.Fatalf("visible roster height = %d, want 1", got)
	}
	if line := strings.TrimSpace(renderRoster(t, 80, entries)); line != "1 agent ▶" {
		t.Fatalf("single-agent roster rendered %q", line)
	}
}

// TestAgentRosterRendersEveryAgent covers the strip's whole job: every agent
// present, each with a jump number and lifecycle indicator, on one line.
func TestAgentRosterRendersEveryAgent(t *testing.T) {
	line := renderRoster(t, 100, []AgentRosterEntry{
		{Name: "chief", State: "RUNNING"},
		{Name: "scout", State: "IDLE"},
		{Name: "docs", State: "PAUSED"},
		{Name: "tests", State: "WAITING_INPUT", WaitingOn: "ok to delete testdata/legacy?"},
		{Name: "bench", State: "FAILED"},
		{Name: "archive", State: "STOPPED"},
	})

	for _, want := range []string{
		"1 chief ▶",
		"2 scout ○",
		"3 docs ⏸",
		"4 tests needs you",
		"5 bench ✘",
		"6 archive ⏹",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("expected %q in roster, got:\n%q", want, line)
		}
	}
	if got := strings.Count(line, "\n"); got != 0 {
		t.Fatalf("roster must stay one line, got %d newlines:\n%q", got, line)
	}
}

// TestAgentRosterStylesFocusAndMarksReachability keeps the switcher facts
// legible with minimal punctuation: focus is styling-only, while an entry
// whose handle the client could not rebuild retains a quiet watch-only mark.
func TestAgentRosterStylesFocusAndMarksReachability(t *testing.T) {
	line := renderRosterWithProfile(t, 100, []AgentRosterEntry{
		{ID: "a", Name: "chief", State: "IDLE"},
		{ID: "b", Name: "scout", State: "RUNNING", Focused: true},
		{ID: "c", Name: "ghost", State: "RUNNING", ReadOnly: true},
	}, termenv.ANSI)
	plain := stripANSICodes(line)

	if !strings.Contains(plain, "2 scout ▶") || strings.Contains(plain, "*") {
		t.Fatalf("expected focus to use styling without a marker, got:\n%q", line)
	}
	if !strings.Contains(plain, "3 ghost·") {
		t.Fatalf("expected the unaddressable agent to be marked, got:\n%q", line)
	}
	if !strings.Contains(line, "\x1b[1m1") {
		t.Fatalf("expected jump numbers to be bold, got:\n%q", line)
	}
}

// TestAgentRosterNumbersOnlyJumpableEntries: the numbers are jump targets
// (ctrl+1…9 from the prompt, 1…9 in nav mode), so an entry past the ninth gets
// none rather than advertising a key that does nothing.
func TestAgentRosterNumbersOnlyJumpableEntries(t *testing.T) {
	entries := make([]AgentRosterEntry, 0, 10)
	for _, name := range []string{
		"a1", "a2", "a3", "a4", "a5", "a6", "a7", "a8", "a9", "a10",
	} {
		entries = append(entries, AgentRosterEntry{Name: name, State: "IDLE"})
	}
	line := renderRoster(t, 400, entries)
	if !strings.Contains(line, "9 a9") {
		t.Fatalf("expected the ninth agent to be numbered, got:\n%q", line)
	}
	if strings.Contains(line, "10 a10") || !strings.Contains(line, "a10 ○") {
		t.Fatalf("the tenth agent must be listed without a jump number, got:\n%q", line)
	}
}

// TestAgentRosterUnknownStateIsQuiet covers the window between an agent's loop
// span appearing and its first state record arriving: the agent is known to
// exist but its state is not, and the strip must not invent one.
func TestAgentRosterUnknownStateIsQuiet(t *testing.T) {
	line := strings.TrimSpace(renderRoster(t, 80, []AgentRosterEntry{
		{Name: "chief", State: "RUNNING"},
		{Name: "fresh"},
	}))
	if want := "1 chief ▶  2 fresh"; line != want {
		t.Fatalf("stateless agent rendered with a lifecycle indicator: got %q, want %q", line, want)
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
