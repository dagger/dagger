package idtui

import (
	"fmt"
	"image/color"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/cellbuf"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/require"
	"github.com/vito/tuist"
)

func renderRoster(t *testing.T, width int, entries []AgentRosterEntry) string {
	return renderRosterWithProfile(t, width, entries, termenv.Ascii)
}

func renderRosterWithProfile(t *testing.T, width int, entries []AgentRosterEntry, profile termenv.Profile) string {
	t.Helper()
	roster := NewAgentRoster(profile, rosterTabs(entries))
	term := tuist.NewHeadlessTerminal(width, 1)
	tui := tuist.New(term)
	tui.AddChild(roster)
	tui.RenderOnce()
	return strings.Join(tui.Frame(), "\n")
}

// rosterTabs is a roster source showing exactly the given tabs.
func rosterTabs(entries []AgentRosterEntry) func() AgentRosterView {
	return func() AgentRosterView {
		return AgentRosterView{Tabs: entries, Total: len(entries)}
	}
}

// TestAgentRosterAlwaysShowsPublishedAgents locks in the roster's dual role as
// switcher and state indicator. A single entry renders, but is not switchable.
func TestAgentRosterAlwaysShowsPublishedAgents(t *testing.T) {
	empty := NewAgentRoster(termenv.Ascii, rosterTabs(nil))
	if empty.Visible() || empty.Switchable() || empty.Height() != 0 {
		t.Fatal("an empty roster should remain hidden")
	}

	entries := []AgentRosterEntry{{Name: "interactive", State: "RUNNING"}}
	roster := NewAgentRoster(termenv.Ascii, rosterTabs(entries))
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
		{Name: "tests", State: "WAITING_INPUT"},
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
	if !strings.Contains(line, "\x1b[90m1") || strings.Contains(line, "\x1b[1m1") {
		t.Fatalf("expected jump numbers to be faint, not bold, got:\n%q", line)
	}
	// Unfocused names read dimmer than the focused one, not brighter.
	if !strings.Contains(line, "\x1b[2mchief") {
		t.Fatalf("expected an unfocused name to be faint, got:\n%q", line)
	}
}

// TestAgentRosterFocusTabSpansEntry: the focused entry is a tab filled across
// its padding, jump number, name and state symbol -- with the prompt card's
// shade when one is known, else reverse video -- rather than highlighting the
// name alone and leaving the number and symbol stranded beside it. Its
// neighbors' padding stays unfilled.
func TestAgentRosterFocusTabSpansEntry(t *testing.T) {
	entries := []AgentRosterEntry{
		{ID: "a", Name: "chief", State: "IDLE"},
		{ID: "b", Name: "scout", State: "RUNNING", Focused: true},
		{ID: "c", Name: "docs", State: "IDLE"},
	}
	shades := blendPromptBackground(color.Black, termenv.TrueColor)
	shade := shades.cell

	for _, tc := range []struct {
		name   string
		colors func() (color.Color, color.Color)
		// sides are the tab's outer cells: padding, or the card's border.
		left, right string
		filled      func(cellbuf.Style) bool
	}{
		{
			name:   "prompt shade",
			colors: func() (color.Color, color.Color) { return shade, nil },
			left:   " ", right: " ",
			filled: func(s cellbuf.Style) bool { return s.Bg == shade },
		},
		{
			name:   "prompt shade and border",
			colors: func() (color.Color, color.Color) { return shade, shades.border },
			left:   rosterTabLeftEdge, right: rosterTabRightEdge,
			filled: func(s cellbuf.Style) bool { return s.Bg == shade },
		},
		{
			name: "no shade known",
			left: " ", right: " ",
			filled: func(s cellbuf.Style) bool { return s.Attrs&cellbuf.ReverseAttr != 0 },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			roster := NewAgentRoster(termenv.TrueColor, rosterTabs(entries))
			if tc.colors != nil {
				roster.SetTabColorSource(tc.colors)
			}
			line := roster.Line(100)
			plain := ansi.Strip(line)
			tab := tc.left + "2 scout " + CaretRightFilled + tc.right
			start := strings.Index(plain, tab)
			if start < 0 {
				t.Fatalf("roster missing the focused entry %q: %q", tab, plain)
			}
			first := ansi.StringWidth(plain[:start])
			last := first + ansi.StringWidth(tab) - 1
			if gotStart, gotEnd, ok := roster.FocusedTab(100); !ok || gotStart != first || gotEnd != last+1 {
				t.Fatalf("FocusedTab = %d..%d (ok=%v), want %d..%d", gotStart, gotEnd, ok, first, last+1)
			}

			buf := cellbuf.NewBuffer(ansi.StringWidth(line), 1)
			cellbuf.SetContent(buf, line)
			for x := range buf.Width() {
				cell := buf.Cell(x, 0)
				if cell == nil {
					continue
				}
				if want := x >= first && x <= last; tc.filled(cell.Style) != want {
					t.Fatalf("cell %d (%q) filled=%v, want %v (tab spans %d..%d): %q",
						x, cell.String(), !want, want, first, last, line)
				}
				if tc.left != " " && (x == first || x == last) && cell.Style.Fg != shades.border {
					t.Fatalf("tab side at cell %d not in the border color: %q", x, line)
				}
			}
		})
	}
}

// TestAgentRosterLeadingTabHasNoLeftEdge: the prompt card draws no left
// border, so a focused tab at column 0 has nothing to continue on its left --
// it keeps a filled padding cell there, and only its right side is an edge.
func TestAgentRosterLeadingTabHasNoLeftEdge(t *testing.T) {
	entries := []AgentRosterEntry{
		{Name: "chief", State: "IDLE", Focused: true},
		{Name: "scout", State: "RUNNING"},
	}
	shades := blendPromptBackground(color.Black, termenv.TrueColor)
	roster := NewAgentRoster(termenv.TrueColor, rosterTabs(entries))
	roster.SetTabColorSource(func() (color.Color, color.Color) { return shades.cell, shades.border })

	line := roster.Line(100)
	tab := " 1 chief " + DotEmpty + rosterTabRightEdge
	if plain := ansi.Strip(line); !strings.HasPrefix(plain, tab) {
		t.Fatalf("expected the leading tab %q without a left edge, got %q", tab, plain)
	}
	if start, end, ok := roster.FocusedTab(100); !ok || start != 0 || end != ansi.StringWidth(tab) {
		t.Fatalf("FocusedTab = %d..%d (ok=%v), want 0..%d", start, end, ok, ansi.StringWidth(tab))
	}
	buf := cellbuf.NewBuffer(ansi.StringWidth(line), 1)
	cellbuf.SetContent(buf, line)
	if cell := buf.Cell(0, 0); cell == nil || cell.Style.Bg != shades.cell {
		t.Fatalf("leading padding cell not filled with the card's shade: %q", line)
	}
}

// TestAgentRosterFocusedTabClipsToTruncation: the prompt card opens its edge
// over the columns FocusedTab reports, so they must match what survives the
// strip's truncation -- never extending over the ellipsis or past it.
func TestAgentRosterFocusedTabClipsToTruncation(t *testing.T) {
	entries := []AgentRosterEntry{
		{Name: "chief", State: "IDLE"},
		{Name: "scout", State: "RUNNING", Focused: true},
		{Name: "docs", State: "IDLE"},
	}
	roster := NewAgentRoster(termenv.Ascii, rosterTabs(entries))
	// " 1 chief ○ " is 11 cells; the focused tab starts there.
	if start, end, ok := roster.FocusedTab(15); !ok || start != 11 || end != 14 {
		t.Fatalf("truncated FocusedTab = %d..%d (ok=%v), want 11..14", start, end, ok)
	}
	if _, _, ok := roster.FocusedTab(11); ok {
		t.Fatal("a focused tab truncated away must not report columns")
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
// span appearing and its first control record arriving: the agent is known to
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

// rosterNode is an agent as db.Agents() reports it, lineage included.
func rosterNode(id, parent, state string) *dagui.AgentNode {
	return &dagui.AgentNode{ID: id, Name: id, ParentID: parent, State: state, CallDigest: "sha256:" + id}
}

// rosterTabNames summarizes tabs: the name, * when focused, then any
// attention badge as +N? (waiting) and +N! (failed).
func rosterTabNames(tabs []AgentRosterEntry) []string {
	names := make([]string, len(tabs))
	for i, tab := range tabs {
		name := tab.Name
		if tab.Focused {
			name += "*"
		}
		if tab.Waiting > 0 {
			name += fmt.Sprintf(" +%d?", tab.Waiting)
		}
		if tab.Failed > 0 {
			name += fmt.Sprintf(" +%d!", tab.Failed)
		}
		names[i] = name
	}
	return names
}

func buildTestRoster(agents []*dagui.AgentNode, focused string) agentRosterModel {
	return buildAgentRosterModel(agents, focused, func(a *dagui.AgentNode) AgentRosterEntry {
		return AgentRosterEntry{ID: a.ID, Name: a.Name, State: a.State}
	})
}

// deepRoster is the shape of a real session (trace 56523ce2…), shrunk: a
// chief whose workers run reviewers, one worker done, a test agent failing
// under another, and stray roots left behind by an integration test's nested
// client -- in the order they appeared, which is not tree order.
func deepRoster() []*dagui.AgentNode {
	return []*dagui.AgentNode{
		rosterNode("agent", "", "IDLE"),
		rosterNode("w1", "agent", "IDLE"),
		rosterNode("w2", "agent", "RUNNING"),
		rosterNode("r1", "w1", "STOPPED"),
		rosterNode("inert", "", "STOPPED"),
		rosterNode("r2", "w1", "RUNNING"),
		rosterNode("w3", "agent", "STOPPED"),
		rosterNode("t1", "w2", "FAILED"),
		rosterNode("r3", "w1", "WAITING_INPUT"),
		rosterNode("probe", "", "IDLE"),
	}
}

// TestAgentRosterModelOrdersByLineage: [/] walks every agent depth-first by
// lineage, so a worker's reviewers follow it rather than wherever they
// happened to appear, and nobody is left out.
func TestAgentRosterModelOrdersByLineage(t *testing.T) {
	model := buildTestRoster(deepRoster(), "agent")
	var names []string
	for _, entry := range model.order {
		names = append(names, entry.Name)
	}
	require.Equal(t, []string{"agent", "w1", "r1", "r2", "r3", "w2", "t1", "w3", "inert", "probe"}, names)
	require.Equal(t, 10, model.view.Total)
}

// TestAgentRosterModelShowsTheNeighborhoodOfFocus: the strip shows the
// roots, the focused agent's ancestors, and its direct children -- not every
// agent in the session -- with what it leaves out that needs the user
// surfacing on the nearest tab above, and STOPPED agents folded.
func TestAgentRosterModelShowsTheNeighborhoodOfFocus(t *testing.T) {
	for _, tc := range []struct {
		name    string
		focused string
		tabs    []string
		done    int
	}{
		{
			// The chief and its workers. r3 waits on the user and t1
			// failed: neither has a tab, so their workers say so. w3 and
			// the stray inert root are done.
			name:    "focus on the chief",
			focused: "agent",
			tabs:    []string{"agent*", "w1 +1?", "w2 +1!", "probe"},
			done:    2,
		},
		{
			// Same view, so the strip does not change shape on first focus.
			name: "nothing focused",
			tabs: []string{"agent", "w1 +1?", "w2 +1!", "probe"},
			done: 2,
		},
		{
			// Down a level: the chain to the reviewer, and the reviewer's
			// siblings now hidden; r3 still bubbles to w1, and t1 -- under
			// a worker no longer shown -- all the way to the chief.
			name:    "focus on a reviewer",
			focused: "r2",
			tabs:    []string{"agent +1!", "w1 +1?", "r2*", "probe"},
			done:    1,
		},
		{
			// A worker: its reviewers, one of them done; the one waiting has
			// its own tab now, so it badges nobody.
			name:    "focus on a worker",
			focused: "w1",
			tabs:    []string{"agent +1!", "w1*", "r2", "r3", "probe"},
			done:    2,
		},
		{
			// Never fold the focused agent, however finished.
			name:    "focus on a stopped agent",
			focused: "r1",
			tabs:    []string{"agent +1!", "w1 +1?", "r1*", "probe"},
			done:    1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := buildTestRoster(deepRoster(), tc.focused)
			require.Equal(t, tc.tabs, rosterTabNames(model.view.Tabs))
			require.Equal(t, tc.done, model.view.Done)
		})
	}
}

// TestAgentRosterModelKeepsFocusContext: the focused agent's ancestors are
// the context it is read in, so they keep their tabs even when STOPPED --
// a live reviewer under a dismissed worker still shows the worker.
func TestAgentRosterModelKeepsFocusContext(t *testing.T) {
	agents := []*dagui.AgentNode{
		rosterNode("agent", "", "IDLE"),
		rosterNode("w", "agent", "STOPPED"),
		rosterNode("r", "w", "RUNNING"),
	}
	model := buildTestRoster(agents, "r")
	require.Equal(t, []string{"agent", "w", "r*"}, rosterTabNames(model.view.Tabs))
	require.Zero(t, model.view.Done)

	// Focus elsewhere and the dismissed worker folds again.
	model = buildTestRoster(agents, "agent")
	require.Equal(t, []string{"agent*"}, rosterTabNames(model.view.Tabs))
	require.Equal(t, 1, model.view.Done)
}

// TestAgentRosterModelUnfoldsToCarryAttention: a folded agent is quiet, so
// it cannot be where attention from beneath lands unseen -- it gets its tab
// back to carry the badge.
func TestAgentRosterModelUnfoldsToCarryAttention(t *testing.T) {
	agents := []*dagui.AgentNode{
		rosterNode("agent", "", "IDLE"),
		rosterNode("w", "agent", "IDLE"),
		rosterNode("old", "", "STOPPED"),
		rosterNode("asking", "old", "IDLE"),
		rosterNode("deep", "asking", "WAITING_INPUT"),
	}
	model := buildTestRoster(agents, "w")
	require.Equal(t, []string{"agent", "w*", "old +1?"}, rosterTabNames(model.view.Tabs))
	require.Zero(t, model.view.Done)
}

// TestAgentRosterRendersFoldsAndBadges: the badges read in the state
// labels' own words, and the folded agents as one quiet, unnumbered chip.
func TestAgentRosterRendersFoldsAndBadges(t *testing.T) {
	roster := NewAgentRoster(termenv.Ascii, func() AgentRosterView {
		return AgentRosterView{
			Tabs: []AgentRosterEntry{
				{Name: "chief", State: "IDLE", Waiting: 2, Failed: 1},
				{Name: "scout", State: "RUNNING"},
			},
			Done:  7,
			Total: 12,
		}
	})
	require.Equal(t, "1 chief ○ +2 needs you +1 ✘  2 scout ▶  +7 done", strings.TrimSpace(roster.Line(0)))
	require.True(t, roster.Switchable())

	// Everything folded still renders, and still switches.
	folded := NewAgentRoster(termenv.Ascii, func() AgentRosterView {
		return AgentRosterView{Done: 3, Total: 3}
	})
	require.True(t, folded.Visible())
	require.True(t, folded.Switchable())
	require.Equal(t, "+3 done", strings.TrimSpace(folded.Line(0)))
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
