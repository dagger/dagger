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
