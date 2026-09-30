package idtui

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/key"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/util/patchpreview"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/require"
	"github.com/vito/tuist"
)

// reactingShellHandler records the keys the frontend hands the shell.
type reactingShellHandler struct {
	stubShellHandler
	mu   sync.Mutex
	keys []string
}

func (h *reactingShellHandler) ReactToInput(_ context.Context, ev uv.KeyPressEvent, _ string, _ bool) func() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.keys = append(h.keys, uv.Key(ev).String())
	return func() {}
}

func (h *reactingShellHandler) reacted() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.keys...)
}

// testPatch is a patch changing 20 lines in each file: long enough to scroll.
func testPatch(files ...string) string {
	var b strings.Builder
	for _, file := range files {
		fmt.Fprintf(&b, "diff --git a/%[1]s b/%[1]s\n--- a/%[1]s\n+++ b/%[1]s\n@@ -1,20 +1,20 @@\n", file)
		for i := range 20 {
			fmt.Fprintf(&b, "-old %s %d\n", file, i)
		}
		for i := range 20 {
			fmt.Fprintf(&b, "+new %s %d\n", file, i)
		}
	}
	return b.String()
}

// TestDiffViewer: ctrl+g opens the Changes bubble's diffs from the prompt,
// the list and patch follow the keys and the bubble's live updates, the
// bubble's own keys still reach the shell, and closing returns to the draft.
func TestDiffViewer(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	fe := newWithTerminal(io.Discard, dagui.NewDB(), tuist.NewHeadlessTerminal(120, 40))
	fe.setupTUI()
	shell := &reactingShellHandler{}
	fe.startShell(context.Background(), shell)
	fe.tui.Step()
	fe.textInput.SetValue("unfinished draft")
	draftFocus := fe.tui.Focused()

	var loads atomic.Int32
	entry := func(id, group, label, title, header, patch string) DiffEntry {
		return DiffEntry{
			ID: id, Group: group, Label: label, Title: title,
			Load: func() (DiffDetail, error) {
				loads.Add(1)
				return DiffDetail{Header: header, Patch: patch}, nil
			},
		}
	}
	uncommitted := entry("uncommitted", "", "", "Uncommitted changes (1 file)", "", testPatch("pending.go"))
	first := entry("aaaaaaa1", "Commits to save", "aaaaaaa", "first commit", "Author: Agent <agent@localhost>", testPatch("one.go", "two.go"))
	second := entry("bbbbbbb2", "Commits to save", "bbbbbbb", "second commit", "", "diff --git a/evil.txt b/evil.txt\n--- a/evil.txt\n+++ b/evil.txt\n@@ -0,0 +1 @@\n+clear\x1b[2Jscreen\n")
	setChanges := func(diffs ...DiffEntry) {
		fe.SetSidebarContent(SidebarSection{
			Title:       "Changes",
			ContentFunc: func(int) string { return "summary" },
			KeyMap:      []key.Binding{key.NewBinding(key.WithKeys("ctrl+s"), key.WithHelp("ctrl+s", "save"))},
			Diffs:       diffs,
		})
	}
	screen := func() string { return stripANSICodes(strings.Join(fe.tui.Step(), "\n")) }
	waitFor := func(want string) string {
		t.Helper()
		var frame string
		require.Eventually(t, func() bool {
			frame = screen()
			return strings.Contains(frame, want)
		}, 5*time.Second, 5*time.Millisecond, "waiting for %q", want)
		return frame
	}
	press := func(keys ...string) string {
		for _, k := range keys {
			fe.tui.Inject(tuist.ParseKey(k))
		}
		return screen()
	}

	require.NotContains(t, navKeyHelp(fe.keys(NewOutput(io.Discard))), "view diff", "no diffs, no key")
	setChanges(uncommitted, first, second)
	frame := screen()
	require.Contains(t, frame, "ctrl+g view diff", "the bubble advertises the viewer")
	require.Contains(t, navKeyHelp(fe.keys(NewOutput(io.Discard))), "ctrl+g view diff")

	// Opens from the prompt, on the first entry, with the others listed.
	press(diffViewerKey)
	require.NotNil(t, fe.diffViewer)
	frame = waitFor("+new pending.go")
	require.Contains(t, frame, "CHANGES")
	require.Contains(t, frame, "Uncommitted changes (1 file)")
	require.Contains(t, frame, "Commits to save 2")
	require.Contains(t, frame, "aaaaaaa first commit")
	require.Contains(t, frame, "pending.go +20 -20", "the patch has a diffstat")
	require.NotContains(t, frame, "summary", "the HUD is hidden behind the viewer")
	require.Equal(t, "unfinished draft", fe.textInput.Value())
	// ...the way ctrl+h hides it: the key says so, and one press shows it.
	require.Contains(t, navKeyHelp(fe.hudKeys()), "ctrl+h show hud")
	require.Contains(t, press("ctrl+h"), "summary")
	require.NotContains(t, press("ctrl+h"), "summary")

	// Selecting a commit shows its header and patch.
	press("down")
	frame = waitFor("+new one.go")
	require.Contains(t, frame, "Author: Agent <agent@localhost>")
	require.Contains(t, frame, "two.go +20 -20")
	require.Contains(t, frame, "2 files changed")
	require.NotContains(t, frame, "+new two.go", "below the fold")

	// In the patch pane, n jumps between files.
	press("enter")
	require.True(t, fe.diffViewer.PatchFocused())
	st := fe.diffViewer.current
	require.Len(t, st.files, 2)
	press("n")
	require.Equal(t, st.patchStart, fe.diffViewer.scroll)
	frame = press("n")
	require.Equal(t, st.patchStart+st.files[1], fe.diffViewer.scroll)
	require.Contains(t, frame, "+new two.go")
	press("N")
	require.Equal(t, st.patchStart, fe.diffViewer.scroll)

	// ] steps to the next commit from the patch; repository data can't
	// drive the terminal.
	press("]")
	waitFor("clear")
	raw := strings.Join(fe.tui.Step(), "\n")
	require.NotContains(t, raw, "\x1b[2J")
	require.Contains(t, raw, "clear\uFFFD[2Jscreen")

	// The list follows the bubble, keeping the selection.
	third := entry("ccccccc3", "Commits to save", "ccccccc", "third commit", "", testPatch("three.go"))
	setChanges(first, second, third)
	frame = screen()
	require.Contains(t, frame, "ccccccc third commit")
	require.NotContains(t, frame, "Uncommitted changes")
	require.Contains(t, frame, "clear\uFFFD[2Jscreen", "still on the selected commit")
	require.Equal(t, "bbbbbbb2", fe.diffViewer.selected)

	// Each entry loads once, however often it is shown.
	press("[", "]", "[", "]")
	require.Eventually(t, func() bool { screen(); return loads.Load() == 4 }, 5*time.Second, 5*time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	screen()
	require.EqualValues(t, 4, loads.Load())

	// The bubble's keys act on what is being reviewed.
	press("ctrl+s")
	require.Equal(t, []string{"ctrl+s"}, shell.reacted())
	press("x")
	require.Equal(t, []string{"ctrl+s"}, shell.reacted(), "only the bubble's keys reach the shell")

	// Nothing left to review: the bubble's own words stand in.
	setChanges()
	frame = screen()
	require.Contains(t, frame, "No changes to review")
	require.Contains(t, frame, "summary")

	// Closing returns to the draft and the HUD.
	setChanges(first)
	frame = press("q")
	require.Nil(t, fe.diffViewer)
	require.Same(t, draftFocus, fe.tui.Focused())
	require.Equal(t, "unfinished draft", fe.textInput.Value())
	require.Contains(t, frame, "summary")

	// Nav mode opens it too, and ctrl+g toggles it closed.
	press("esc", diffViewerKey)
	require.NotNil(t, fe.diffViewer)
	press(diffViewerKey)
	require.Nil(t, fe.diffViewer)
}

// TestDiffViewerMouse: like the tests view, the sidebar follows the mouse --
// hover highlights, clicks select, the wheel steps -- and the wheel scrolls
// the patch, all through tuist's positional dispatch.
func TestDiffViewerMouse(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	fe := newWithTerminal(io.Discard, dagui.NewDB(), tuist.NewHeadlessTerminal(120, 40))
	fe.setupTUI()
	fe.startShell(context.Background(), &stubShellHandler{})
	fe.tui.Step()
	entry := func(id, group, label, title, patch string) DiffEntry {
		return DiffEntry{ID: id, Group: group, Label: label, Title: title,
			Load: func() (DiffDetail, error) { return DiffDetail{Patch: patch}, nil }}
	}
	fe.SetSidebarContent(SidebarSection{
		Title: "Changes",
		Diffs: []DiffEntry{
			entry("uncommitted", "", "", "Uncommitted changes", testPatch("pending.go")),
			entry("aaaaaaa1", "Commits to save", "aaaaaaa", "first commit", testPatch("one.go", "two.go")),
			entry("bbbbbbb2", "Commits to save", "bbbbbbb", "second commit", testPatch("three.go")),
		},
	})
	fe.tui.Step()
	fe.tui.Inject(tuist.ParseKey(diffViewerKey))
	screen := func() string { return stripANSICodes(strings.Join(fe.tui.Step(), "\n")) }
	screen()
	v := fe.diffViewer
	require.NotNil(t, v)

	// Screen rows of each entry: the viewer is the top of the frame.
	rowOf := func(idx int) int {
		for row, entry := range v.rowByLine {
			if entry == idx {
				return row
			}
		}
		t.Fatalf("entry %d not in the sidebar", idx)
		return -1
	}
	sidebarX, patchX := 5, v.leftWidth+10
	mouse := func(ev uv.Event) string {
		fe.tui.Inject(ev)
		return screen()
	}

	// Hover highlights sidebar entries, and leaving the sidebar clears it.
	mouse(uv.MouseMotionEvent{X: sidebarX, Y: rowOf(2)})
	require.Equal(t, 2, v.hovered)
	mouse(uv.MouseMotionEvent{X: patchX, Y: rowOf(2)})
	require.Equal(t, -1, v.hovered)

	// Clicking an entry selects it.
	frame := mouse(uv.MouseClickEvent{X: sidebarX, Y: rowOf(1), Button: uv.MouseLeft})
	require.Equal(t, "aaaaaaa1", v.selected)
	require.Contains(t, frame, "▶ aaaaaaa first commit")

	// The wheel scrolls the patch under it...
	require.Eventually(t, func() bool { screen(); return v.current != nil && !v.current.loading }, 5*time.Second, 5*time.Millisecond)
	mouse(uv.MouseWheelEvent{X: patchX, Y: 10, Button: uv.MouseWheelDown})
	require.Equal(t, diffWheelLines, v.scroll)
	mouse(uv.MouseWheelEvent{X: patchX, Y: 10, Button: uv.MouseWheelUp})
	require.Zero(t, v.scroll)

	// ...and steps through the list under it.
	mouse(uv.MouseWheelEvent{X: sidebarX, Y: 10, Button: uv.MouseWheelDown})
	require.Equal(t, "bbbbbbb2", v.selected)
	mouse(uv.MouseWheelEvent{X: sidebarX, Y: 10, Button: uv.MouseWheelUp})
	require.Equal(t, "aaaaaaa1", v.selected)

	// Clicking the patch focuses it for the keys; a sidebar click comes back.
	mouse(uv.MouseClickEvent{X: patchX, Y: 10, Button: uv.MouseLeft})
	require.True(t, v.PatchFocused())
	mouse(uv.MouseClickEvent{X: sidebarX, Y: rowOf(0), Button: uv.MouseLeft})
	require.False(t, v.PatchFocused())
	require.Equal(t, "uncommitted", v.selected)
}

// TestDiffViewerFollowsAgentFocus: the viewer replaces the prompt, keeps the
// status line, and takes prompt mode's agent keys, following the focused
// agent's changes -- without ever passing the agent it left's off as the new
// one's while they load.
func TestDiffViewerFollowsAgentFocus(t *testing.T) {
	runFocusTest(t, func(t *testing.T) {
		handler := &focusShellHandler{target: "agent-chief"}
		fe := focusTestFrontend(t, rosterDB(t), handler)
		fe.textInput.SetValue("draft for the chief")
		screen := func() string { return stripANSICodes(strings.Join(fe.tui.Step(), "\n")) }
		changes := func(agent, subject string) {
			fe.SetSidebarContent(SidebarSection{
				Title:   "Changes",
				Agent:   agent,
				Content: "1 commit to save",
				Diffs: []DiffEntry{{
					ID: subject, Group: "Commits to save", Label: "abc1234", Title: subject,
					Load: func() (DiffDetail, error) { return DiffDetail{Patch: testPatch(subject + ".go")}, nil },
				}},
			})
		}
		press := func(k uv.Key) string {
			fe.handleNavKeyUV(uv.KeyPressEvent(k))
			return screen()
		}

		changes("agent-chief", "chief commit")
		frame := screen()
		require.Contains(t, frame, "draft for the chief")

		// The prompt makes way; the status line with the roster stays.
		require.True(t, fe.toggleDiffViewer())
		frame = screen()
		require.Contains(t, frame, "abc1234 chief commit")
		require.NotContains(t, frame, "draft for the chief")
		require.NotContains(t, frame, "ctrl+? show keymap")
		require.Contains(t, frame, "chief", "the roster is still on screen")
		require.Contains(t, frame, "scout")
		help := navKeyHelp(fe.keys(NewOutput(io.Discard)))
		require.Contains(t, help, "ctrl+1…9 focus agent")
		require.Contains(t, help, "alt+[/] prev/next agent")

		// ctrl+2 moves focus, and the chief's changes go at once.
		frame = press(uv.Key{Code: '2', Mod: uv.ModCtrl})
		require.NotContains(t, frame, "chief commit")
		require.Contains(t, frame, "Loading changes…")
		awaitFocus(t, fe, handler, "agent-scout")
		require.NotContains(t, screen(), "chief commit")

		// A repaint of the chief's changes still in flight from before the
		// switch doesn't pass for the scout's -- before the scout's changes
		// arrive, or after (a turn ending just after the switch).
		changes("agent-chief", "chief commit")
		require.NotContains(t, screen(), "chief commit")
		changes("agent-scout", "scout commit")
		frame = screen()
		require.Contains(t, frame, "abc1234 scout commit")
		require.NotNil(t, fe.diffViewer, "switching agents keeps the viewer open")
		changes("agent-chief", "chief commit")
		frame = screen()
		require.Contains(t, frame, "abc1234 scout commit")
		require.NotContains(t, frame, "chief commit")

		// alt+[ walks back.
		press(uv.Key{Code: '[', Mod: uv.ModAlt})
		awaitFocus(t, fe, handler, "agent-scout", "agent-chief")
		require.Contains(t, screen(), "Loading changes…")
		changes("agent-chief", "chief commit")
		require.Contains(t, screen(), "abc1234 chief commit")

		// A switch that fails rolls back to the agent still in focus, and
		// says why: the error line stays while the prompt is hidden.
		handler.mu.Lock()
		handler.focusErr = fmt.Errorf("no route to scout")
		handler.mu.Unlock()
		press(uv.Key{Code: '2', Mod: uv.ModCtrl})
		awaitFocus(t, fe, handler, "agent-scout", "agent-chief")
		frame = screen()
		require.Contains(t, frame, "abc1234 chief commit")
		require.Contains(t, frame, "no route to scout")

		// Closing brings the prompt back, with the chief's draft and focus.
		press(uv.Key{Code: 'q', Text: "q"})
		require.Nil(t, fe.diffViewer)
		frame = screen()
		require.True(t, fe.inputFocused(), "typing goes to the prompt again")
		require.Contains(t, frame, "draft for the chief")

		// The bubble, too, drops the agent focus left at once. (The failed
		// switch marked the scout read-only; naming it by key retries.)
		require.Contains(t, frame, "ctrl+g view diff")
		handler.mu.Lock()
		handler.focusErr = nil
		handler.mu.Unlock()
		require.True(t, pressEditlineKey(t, fe, uv.Key{Code: '2', Mod: uv.ModCtrl}))
		require.NotContains(t, screen(), "ctrl+g view diff")
		awaitFocus(t, fe, handler, "agent-scout", "agent-chief", "agent-scout")
		changes("agent-scout", "scout commit")
		require.Contains(t, screen(), "ctrl+g view diff")
	})
}

func TestNotificationBottomBorderWidth(t *testing.T) {
	fe := newWithTerminal(io.Discard, dagui.NewDB(), tuist.NewHeadlessTerminal(120, 20))
	fe.profile = termenv.ANSI
	n := newNotificationBubble(fe, SidebarSection{Title: "Changes", Diffs: []DiffEntry{{ID: "x"}}})
	for innerWidth := 1; innerWidth <= 60; innerWidth++ {
		bottom := n.buildBottomBorder(fe.profile, termenv.ANSIBrightBlack, innerWidth)
		require.Equal(t, innerWidth+2, tuist.VisibleWidth(bottom), "innerWidth=%d: %q", innerWidth, stripANSICodes(bottom))
		plain := []rune(stripANSICodes(bottom))
		require.Equal(t, []rune(CornerBottomLeft)[0], plain[0])
		require.Equal(t, []rune(CornerBottoRight)[0], plain[len(plain)-1])
	}
	require.Contains(t, stripANSICodes(n.buildBottomBorder(fe.profile, termenv.ANSIBrightBlack, 40)), "ctrl+g view diff")
}

func TestDiffStatEntries(t *testing.T) {
	patch := strings.Join([]string{
		"diff --git a/new.go b/new.go",
		"new file mode 100644",
		"--- /dev/null",
		"+++ b/new.go",
		"@@ -0,0 +1,2 @@",
		"+package x",
		"+--- not a header",
		"diff --git a/gone.go b/gone.go",
		"deleted file mode 100644",
		"--- a/gone.go",
		"+++ /dev/null",
		"@@ -1 +0,0 @@",
		"-package y",
		"diff --git a/old.go b/moved.go",
		"similarity index 90%",
		"rename from old.go",
		"rename to moved.go",
		"--- a/old.go",
		"+++ b/moved.go",
		"@@ -1,2 +1,2 @@",
		" package z",
		"-var a",
		"+var b",
		`\ No newline at end of file`,
	}, "\n")
	require.Equal(t, []patchpreview.Entry{
		{Path: "new.go", Kind: patchpreview.KindAdded, Added: 2},
		{Path: "gone.go", Kind: patchpreview.KindRemoved, Removed: 1},
		{Path: "moved.go", OldPath: "old.go", Kind: patchpreview.KindRenamed, Added: 1, Removed: 1},
	}, diffStatEntries(strings.Split(patch, "\n")))
}

func TestScrollWindow(t *testing.T) {
	for n := 0; n <= 12; n++ {
		for height := 1; height <= 8; height++ {
			for selected := 0; selected < n; selected++ {
				start, end, top, bottom := scrollWindow(n, selected, height)
				lines := end - start
				if top {
					lines++
				}
				if bottom {
					lines++
				}
				require.LessOrEqual(t, lines, height, "n=%d height=%d selected=%d", n, height, selected)
				require.True(t, selected >= start && selected < end, "n=%d height=%d selected=%d: [%d,%d)", n, height, selected, start, end)
				if top {
					require.Positive(t, start)
				}
				if bottom {
					require.Less(t, end, n)
				}
			}
		}
	}
}
