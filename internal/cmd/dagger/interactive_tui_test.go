package daggercmd

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine/session/terminal"
)

// newTestExplorer builds an explorer model without a live session or API
// client; stdin writes land in the returned channel.
func newTestExplorer(t *testing.T) (*explorerModel, chan []byte) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	stdin := make(chan []byte, 64)
	m := &explorerModel{
		session: &terminal.SessionHandle{Info: &terminal.SessionInfo{
			Workdir:       "/src",
			FromExecError: true,
			TerminalId:    "term",
		}},
		ctx:     ctx,
		cancel:  cancel,
		workdir: "/src",
		root:    &treeNode{name: "/src", rel: ".", isDir: true, expanded: true},
		focus:   focusTerm,
		stdinQ:  stdin,
	}
	return m, stdin
}

func update(t *testing.T, m *explorerModel, msg tea.Msg) tea.Cmd {
	t.Helper()
	_, cmd := m.Update(msg)
	return cmd
}

func recvStdin(t *testing.T, ch chan []byte) string {
	t.Helper()
	select {
	case b := <-ch:
		return string(b)
	case <-time.After(2 * time.Second):
		t.Fatal("expected bytes on the session's stdin")
		return ""
	}
}

func runeKey(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestExplorerAnswersTerminalQueries(t *testing.T) {
	t.Parallel()
	// Programs probe the terminal at startup (termenv sends OSC 11 + DSR and
	// waits up to 5s for a reply). The embedded terminal must answer like a
	// real one, or the shell prompt is delayed by the timeout.
	m, stdin := newTestExplorer(t)
	update(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})

	update(t, m, termOutputMsg("\x1b]11;?\x1b\\\x1b[6n"))
	require.Equal(t, "\x1b[1;1R", recvStdin(t, stdin), "cursor position report")

	update(t, m, termOutputMsg("abc\x1b[6n"))
	require.Equal(t, "\x1b[1;4R", recvStdin(t, stdin))
}

func TestExplorerBuffersOutputBeforeLayout(t *testing.T) {
	t.Parallel()
	m, _ := newTestExplorer(t)
	update(t, m, termOutputMsg("early output"))
	require.Nil(t, m.vt)

	update(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	require.NotNil(t, m.vt)
	require.Contains(t, m.viewTerminal(m.paneRows())[0], "early output")
}

func TestExplorerCursorFollowsFocus(t *testing.T) {
	t.Parallel()
	m, _ := newTestExplorer(t)
	update(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	update(t, m, termOutputMsg("$ "))

	withCursor := m.viewTerminal(m.paneRows())[0]

	update(t, m, tea.KeyMsg{Type: tea.KeyCtrlQ}) // focus the tree
	require.Equal(t, focusTree, m.focus)
	withoutCursor := m.viewTerminal(m.paneRows())[0]
	require.NotEqual(t, withCursor, withoutCursor, "the cursor is drawn only while the terminal is focused")

	update(t, m, tea.KeyMsg{Type: tea.KeyCtrlQ}) // back to the terminal
	require.Equal(t, withCursor, m.viewTerminal(m.paneRows())[0])

	// A program hiding the cursor (vim, less, ...) is honored.
	update(t, m, termOutputMsg("\x1b[?25l"))
	require.Equal(t, withoutCursor, m.viewTerminal(m.paneRows())[0])
}

func TestExplorerForwardsKeysToFocusedTerminal(t *testing.T) {
	t.Parallel()
	m, stdin := newTestExplorer(t)
	update(t, m, runeKey("q"))
	require.Equal(t, "q", recvStdin(t, stdin), "keys go to the shell while the terminal is focused")

	update(t, m, tea.KeyMsg{Type: tea.KeyCtrlQ})
	cmd := update(t, m, runeKey("q"))
	require.NotNil(t, cmd, "q quits from the tree")
	require.Equal(t, tea.Quit(), cmd())
	require.Empty(t, stdin)
}

func TestExplorerStdinNeverBlocks(t *testing.T) {
	t.Parallel()
	m, _ := newTestExplorer(t)
	m.stdinQ = make(chan []byte, 1) // nobody draining
	done := make(chan struct{})
	go func() {
		m.sendStdin([]byte("a"))
		m.sendStdin([]byte("b")) // full: dropped rather than stalling the UI
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("sendStdin blocked")
	}
}

func TestExplorerSmallWindow(t *testing.T) {
	t.Parallel()
	require.True(t, explorerFits(80, 24))
	require.True(t, explorerFits(explorerMinCols, explorerMinRows))
	require.False(t, explorerFits(explorerMinCols-1, 24))
	require.False(t, explorerFits(80, explorerMinRows-1))

	m, _ := newTestExplorer(t)
	update(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	update(t, m, tea.KeyMsg{Type: tea.KeyCtrlQ})
	require.Equal(t, focusTree, m.focus)

	// Shrinking below the split-view minimum hides the tree, gives the
	// terminal the full width, and moves focus back to it.
	update(t, m, tea.WindowSizeMsg{Width: 40, Height: 8})
	require.Zero(t, m.treeWidth())
	require.Equal(t, focusTerm, m.focus)
	require.Equal(t, 40, m.termCols)

	update(t, m, tea.KeyMsg{Type: tea.KeyCtrlQ})
	require.Equal(t, focusTerm, m.focus)
	require.Contains(t, m.status, "too small")
	require.NotContains(t, m.View(), "│", "no tree separator when the tree is hidden")
}

func TestExplorerExportPrompt(t *testing.T) {
	t.Parallel()

	t.Run("defaults to ./<name>", func(t *testing.T) {
		m, _ := newTestExplorer(t)
		m.openExportPrompt("dir/file.txt", false)
		require.Equal(t, "./file.txt", string(m.prompt.value))

		m.openExportPrompt(".", true)
		require.Equal(t, "./src", string(m.prompt.value))

		m.workdir = "/"
		m.openExportPrompt(".", true)
		require.Equal(t, "./rootfs", string(m.prompt.value))
	})

	t.Run("editing", func(t *testing.T) {
		m, _ := newTestExplorer(t)
		m.openExportPrompt("a/b", true)
		update(t, m, tea.KeyMsg{Type: tea.KeyCtrlU})
		require.Empty(t, m.prompt.value)
		for _, s := range []string{"/", "t", "m", "p", "/", "o", "u", "t"} {
			update(t, m, runeKey(s))
		}
		require.Equal(t, "/tmp/out", string(m.prompt.value))
		update(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
		require.Equal(t, "/tmp/ou", string(m.prompt.value))
		update(t, m, tea.KeyMsg{Type: tea.KeyCtrlW})
		require.Equal(t, "/tmp/", string(m.prompt.value))
		update(t, m, tea.KeyMsg{Type: tea.KeySpace})
		require.Equal(t, "/tmp/ ", string(m.prompt.value))
	})

	t.Run("prompt captures keys even with the terminal focused", func(t *testing.T) {
		m, stdin := newTestExplorer(t)
		m.openExportPrompt("x", false)
		update(t, m, runeKey("z"))
		require.Empty(t, stdin)
		require.True(t, strings.HasSuffix(string(m.prompt.value), "z"))
	})

	t.Run("esc cancels", func(t *testing.T) {
		m, _ := newTestExplorer(t)
		m.openExportPrompt("x", false)
		update(t, m, tea.KeyMsg{Type: tea.KeyEsc})
		require.Nil(t, m.prompt)
		require.False(t, m.exporting)
	})

	t.Run("enter with an empty path does nothing", func(t *testing.T) {
		m, _ := newTestExplorer(t)
		m.openExportPrompt("x", false)
		update(t, m, tea.KeyMsg{Type: tea.KeyCtrlU})
		cmd := update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
		require.Nil(t, cmd)
		require.Nil(t, m.prompt)
		require.False(t, m.exporting)
	})

	t.Run("enter starts the export", func(t *testing.T) {
		m, _ := newTestExplorer(t)
		m.openExportPrompt("x", false)
		cmd := update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
		require.NotNil(t, cmd)
		require.True(t, m.exporting)
		require.Contains(t, m.status, "exporting x")

		// no second export while one is running
		m.openExportPrompt("y", false)
		require.Nil(t, m.prompt)
	})

	t.Run("export results", func(t *testing.T) {
		m, _ := newTestExplorer(t)
		m.exporting = true
		update(t, m, exportDoneMsg{rel: "x", dest: "/abs/x"})
		require.False(t, m.exporting)
		require.Equal(t, "exported x → /abs/x", m.status)

		m.exporting = true
		update(t, m, exportDoneMsg{rel: "x", err: errors.New("boom")})
		require.False(t, m.exporting)
		require.Equal(t, "export failed", m.status)
		require.Contains(t, m.loadErr, "boom")
	})
}

func TestExplorerRefresh(t *testing.T) {
	t.Parallel()

	t.Run("unsupported by older engines", func(t *testing.T) {
		m, _ := newTestExplorer(t)
		m.session.Info.TerminalId = ""
		require.Nil(t, m.refresh())
		require.Contains(t, m.status, "unsupported")
	})

	t.Run("one refresh at a time", func(t *testing.T) {
		m, _ := newTestExplorer(t)
		require.NotNil(t, m.refresh())
		require.True(t, m.refreshing)
		require.Nil(t, m.refresh())
	})

	t.Run("failure is reported", func(t *testing.T) {
		m, _ := newTestExplorer(t)
		m.refreshing = true
		update(t, m, snapshotReadyMsg{err: errors.New("nope")})
		require.False(t, m.refreshing)
		require.Equal(t, "refresh failed", m.status)
		require.Contains(t, m.loadErr, "nope")
	})

	t.Run("F5 works from the terminal pane", func(t *testing.T) {
		m, stdin := newTestExplorer(t)
		cmd := update(t, m, tea.KeyMsg{Type: tea.KeyF5})
		require.NotNil(t, cmd)
		require.True(t, m.refreshing)
		require.Empty(t, stdin, "F5 must not reach the shell")
	})
}

func TestExplorerTreeLoading(t *testing.T) {
	t.Parallel()

	t.Run("entries: dirs first, sorted, paths joined", func(t *testing.T) {
		parent := &treeNode{rel: "sub", depth: 1}
		children := buildChildren(parent, []string{"b.txt", "zdir/", "a.txt", "adir/", ""})
		var got []string
		for _, c := range children {
			got = append(got, c.rel)
			require.Equal(t, 2, c.depth)
		}
		require.Equal(t, []string{"sub/adir", "sub/zdir", "sub/a.txt", "sub/b.txt"}, got)
		require.True(t, children[0].isDir)
		require.False(t, children[2].isDir)
	})

	t.Run("stale directory loads are dropped", func(t *testing.T) {
		m, _ := newTestExplorer(t)
		m.gen = 2
		update(t, m, dirLoadedMsg{rel: ".", entries: []string{"stale"}, gen: 1})
		require.Empty(t, m.root.children)
		update(t, m, dirLoadedMsg{rel: ".", entries: []string{"fresh"}, gen: 2})
		require.Len(t, m.root.children, 1)
		require.Equal(t, "fresh", m.root.children[0].name)
	})

	t.Run("previewed file removed in the terminal", func(t *testing.T) {
		m, _ := newTestExplorer(t)
		m.previewPath = "gone.txt"
		m.previewText = []string{"old"}
		update(t, m, fileLoadedMsg{rel: "gone.txt", err: errors.New("no such file")})
		require.Equal(t, []string{"(file no longer exists)"}, m.previewText)
	})

	t.Run("refreshing the preview keeps the scroll position", func(t *testing.T) {
		m, _ := newTestExplorer(t)
		m.previewPath = "f.txt"
		m.previewOff = 2
		update(t, m, fileLoadedMsg{rel: "f.txt", contents: "1\n2\n3\n4"})
		require.Equal(t, 2, m.previewOff)
		update(t, m, fileLoadedMsg{rel: "f.txt", contents: "only"})
		require.Equal(t, 0, m.previewOff, "clamped when the file shrinks")
	})
}

func TestSanitizePreview(t *testing.T) {
	t.Parallel()
	require.Equal(t, "a\tb\nc", sanitizePreview("a\tb\nc"))
	require.Equal(t, "bell·x", sanitizePreview("bell\x07x"))
	require.Equal(t, "(binary file)", sanitizePreview("\x00\x01\x02\x03\x04ab"))
	require.Equal(t, "(binary file)", sanitizePreview("mostly text but\x00one NUL"))
	// colored logs are text: escapes are neutralized, not treated as binary
	require.Equal(t, "·[31merror·[0m: boom", sanitizePreview("\x1b[31merror\x1b[0m: boom"))
	require.Equal(t, "", sanitizePreview(""))
}

func TestKeyToBytes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		msg  tea.KeyMsg
		want string
	}{
		{runeKey("ls"), "ls"},
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x"), Alt: true}, "\x1bx"},
		{tea.KeyMsg{Type: tea.KeyEnter}, "\r"},
		{tea.KeyMsg{Type: tea.KeySpace}, " "},
		{tea.KeyMsg{Type: tea.KeyBackspace}, "\x7f"},
		{tea.KeyMsg{Type: tea.KeyTab}, "\t"},
		{tea.KeyMsg{Type: tea.KeyShiftTab}, "\x1b[Z"},
		{tea.KeyMsg{Type: tea.KeyEsc}, "\x1b"},
		{tea.KeyMsg{Type: tea.KeyCtrlC}, "\x03"},
		{tea.KeyMsg{Type: tea.KeyCtrlD}, "\x04"},
		{tea.KeyMsg{Type: tea.KeyCtrlR}, "\x12"}, // reverse-search stays with the shell
		{tea.KeyMsg{Type: tea.KeyUp}, "\x1b[A"},
		{tea.KeyMsg{Type: tea.KeyDown}, "\x1b[B"},
		{tea.KeyMsg{Type: tea.KeyRight}, "\x1b[C"},
		{tea.KeyMsg{Type: tea.KeyLeft}, "\x1b[D"},
		{tea.KeyMsg{Type: tea.KeyHome}, "\x1b[H"},
		{tea.KeyMsg{Type: tea.KeyEnd}, "\x1b[F"},
		{tea.KeyMsg{Type: tea.KeyPgUp}, "\x1b[5~"},
		{tea.KeyMsg{Type: tea.KeyPgDown}, "\x1b[6~"},
		{tea.KeyMsg{Type: tea.KeyDelete}, "\x1b[3~"},
		{tea.KeyMsg{Type: tea.KeyF1}, "\x1bOP"},
	} {
		require.Equal(t, tc.want, string(keyToBytes(tc.msg)), "key %v", tc.msg)
	}
}
