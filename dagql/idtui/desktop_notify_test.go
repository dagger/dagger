package idtui

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
	"github.com/vito/tuist"

	"github.com/dagger/dagger/dagql/dagui"
)

func envFrom(env map[string]string) func(string) string {
	return func(k string) string { return env[k] }
}

func TestDetectNotifyChannel(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		want notifyChannel
	}{
		{"unknown terminal", map[string]string{"TERM": "xterm-256color"}, notifyOff},
		{"no environment", nil, notifyOff},
		{"dumb terminal", map[string]string{"TERM": "dumb", "TERM_PROGRAM": "iTerm.app"}, notifyOff},
		{"iterm2", map[string]string{"TERM_PROGRAM": "iTerm.app"}, notifyITerm2},
		{"iterm2 under tmux", map[string]string{"TERM_PROGRAM": "tmux", "LC_TERMINAL": "iTerm2", "TMUX": "/tmp/tmux"}, notifyITerm2},
		{"kitty by TERM", map[string]string{"TERM": "xterm-kitty"}, notifyKitty},
		{"kitty under tmux", map[string]string{"TERM": "tmux-256color", "TERM_PROGRAM": "tmux", "KITTY_WINDOW_ID": "1"}, notifyKitty},
		{"ghostty by TERM_PROGRAM", map[string]string{"TERM_PROGRAM": "ghostty"}, notifyGhostty},
		{"ghostty by TERM", map[string]string{"TERM": "xterm-ghostty"}, notifyGhostty},
		{"ghostty under tmux", map[string]string{"TERM_PROGRAM": "tmux", "GHOSTTY_RESOURCES_DIR": "/usr/share/ghostty"}, notifyGhostty},
		{"ghostty wins over inherited kitty id", map[string]string{"TERM_PROGRAM": "ghostty", "KITTY_WINDOW_ID": "3"}, notifyGhostty},
		{"wezterm", map[string]string{"TERM_PROGRAM": "WezTerm"}, notifyITerm2},
		{"apple terminal", map[string]string{"TERM_PROGRAM": "Apple_Terminal"}, notifyBell},
		{"vscode", map[string]string{"TERM_PROGRAM": "vscode"}, notifyOff},

		{"explicit off", map[string]string{notifyEnvVar: "off", "TERM_PROGRAM": "iTerm.app"}, notifyOff},
		{"claude's off spelling", map[string]string{notifyEnvVar: "notifications_disabled", "TERM": "xterm-kitty"}, notifyOff},
		{"explicit bell", map[string]string{notifyEnvVar: "bell"}, notifyBell},
		{"claude's bell spelling", map[string]string{notifyEnvVar: "terminal_bell"}, notifyBell},
		{"explicit iterm2", map[string]string{notifyEnvVar: "iterm2", "TERM": "xterm-kitty"}, notifyITerm2},
		{"explicit iterm2 with bell", map[string]string{notifyEnvVar: "iterm2_with_bell"}, notifyITerm2WithBell},
		{"explicit kitty", map[string]string{notifyEnvVar: "Kitty"}, notifyKitty},
		{"explicit ghostty", map[string]string{notifyEnvVar: " ghostty "}, notifyGhostty},
		{"explicit auto", map[string]string{notifyEnvVar: "auto", "TERM": "xterm-kitty"}, notifyKitty},
		{"typo falls back to auto", map[string]string{notifyEnvVar: "itemr2", "TERM": "xterm-kitty"}, notifyKitty},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, detectNotifyChannel(envFrom(tc.env)), "got %s", detectNotifyChannel(envFrom(tc.env)))
		})
	}
}

func TestDetectNotifyMux(t *testing.T) {
	require.Equal(t, muxNone, detectNotifyMux(envFrom(nil)))
	require.Equal(t, muxTmux, detectNotifyMux(envFrom(map[string]string{"TMUX": "/tmp/tmux-1000/default,1,0"})))
	require.Equal(t, muxScreen, detectNotifyMux(envFrom(map[string]string{"STY": "1234.pts-0.host"})))
	// screen nested in tmux: tmux is the outermost layer we talk to
	require.Equal(t, muxTmux, detectNotifyMux(envFrom(map[string]string{"TMUX": "x", "STY": "y"})))
}

func TestEncodeNotification(t *testing.T) {
	for _, tc := range []struct {
		name  string
		ch    notifyChannel
		mux   notifyMux
		title string
		body  string
		want  string
	}{
		{"iterm2", notifyITerm2, muxNone, "Dagger", "chief finished its turn",
			"\x1b]9;Dagger: chief finished its turn\a"},
		{"iterm2 title only", notifyITerm2, muxNone, "Dagger", "",
			"\x1b]9;Dagger\a"},
		{"iterm2 with bell", notifyITerm2WithBell, muxNone, "Dagger", "hi",
			"\x1b]9;Dagger: hi\a\a"},
		{"kitty", notifyKitty, muxNone, "Dagger", "hi",
			"\x1b]99;i=dagger-7:d=0;Dagger\x1b\\\x1b]99;i=dagger-7:p=body;hi\x1b\\"},
		{"kitty title only", notifyKitty, muxNone, "Dagger", "",
			"\x1b]99;i=dagger-7;Dagger\x1b\\"},
		{"ghostty", notifyGhostty, muxNone, "Dagger", "a;b",
			"\x1b]777;notify;Dagger;a;b\a"},
		{"ghostty title keeps its field", notifyGhostty, muxNone, "x;y", "b",
			"\x1b]777;notify;x,y;b\a"},
		{"bell", notifyBell, muxNone, "Dagger", "hi", "\a"},
		{"off", notifyOff, muxNone, "Dagger", "hi", ""},
		{"empty title promotes body", notifyITerm2, muxNone, "", "hi", "\x1b]9;hi\a"},
		{"nothing to say", notifyBell, muxNone, " ", "\n", ""},

		{"iterm2 in tmux", notifyITerm2, muxTmux, "Dagger", "hi",
			"\x1bPtmux;\x1b\x1b]9;Dagger: hi\a\x1b\\"},
		{"iterm2 with bell in tmux leaves BEL bare", notifyITerm2WithBell, muxTmux, "Dagger", "hi",
			"\x1bPtmux;\x1b\x1b]9;Dagger: hi\a\x1b\\\a"},
		{"kitty in tmux doubles every ESC", notifyKitty, muxTmux, "Dagger", "hi",
			"\x1bPtmux;\x1b\x1b]99;i=dagger-7:d=0;Dagger\x1b\x1b\\\x1b\\" +
				"\x1bPtmux;\x1b\x1b]99;i=dagger-7:p=body;hi\x1b\x1b\\\x1b\\"},
		{"bell in tmux", notifyBell, muxTmux, "Dagger", "hi", "\a"},
		{"ghostty in screen", notifyGhostty, muxScreen, "Dagger", "hi",
			"\x1bP\x1b]777;notify;Dagger;hi\a\x1b\\"},
		{"kitty in screen swaps ST for BEL", notifyKitty, muxScreen, "Dagger", "hi",
			"\x1bP\x1b]99;i=dagger-7:d=0;Dagger\a\x1b\\\x1bP\x1b]99;i=dagger-7:p=body;hi\a\x1b\\"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, encodeNotification(tc.ch, tc.mux, 7, tc.title, tc.body))
		})
	}
}

// TestEncodeNotificationSanitizes: hostile text cannot end the sequence early
// or smuggle its own escapes, whatever the channel.
func TestEncodeNotificationSanitizes(t *testing.T) {
	hostile := "evil\x07\x1b]0;pwned\x1b\\\x9c name\r\nnext\u202eline"
	for _, ch := range []notifyChannel{notifyITerm2, notifyKitty, notifyGhostty} {
		for _, mux := range []notifyMux{muxNone, muxTmux, muxScreen} {
			out := encodeNotification(ch, mux, 1, hostile, hostile)
			require.NotContains(t, out, "pwned\x1b", "ch=%s mux=%d", ch, mux)
			require.NotContains(t, out, "\x9c")
			require.NotContains(t, out, "\u202e")
			require.NotContains(t, out, "\r")
			require.NotContains(t, out, "\n")
			require.Contains(t, out, "evil]0;pwned\\ name next")
		}
	}
}

func TestSanitizeNotifyText(t *testing.T) {
	require.Equal(t, "a b c", sanitizeNotifyText("  a \t b\n\n c  ", 10))
	require.Equal(t, "ab", sanitizeNotifyText("a\x1b\x07\x7f\u009b\u200bb", 10))
	require.Equal(t, "a b", sanitizeNotifyText("a\u0085b", 10), "NEL is a line break")
	require.Equal(t, "héllo", sanitizeNotifyText("héllo", 5))
	require.Equal(t, "héll…", sanitizeNotifyText("héllo!", 5))
	require.Equal(t, "ab…", sanitizeNotifyText("ab cdef", 4), "no dangling space before the ellipsis")
	require.Equal(t, "", sanitizeNotifyText("anything", 0))
	long := strings.Repeat("x", 1000)
	require.Len(t, []rune(sanitizeNotifyText(long, maxNotifyBodyRunes)), maxNotifyBodyRunes)
	require.Equal(t, "ab", sanitizeNotifyText("a\xffb", 10), "invalid UTF-8 is dropped")
}

// fakeNotifier records what a notifier writes, on a controllable clock.
type fakeNotifier struct {
	*desktopNotifier
	out   []string
	clock time.Time
}

func newFakeNotifier(ch notifyChannel) *fakeNotifier {
	f := &fakeNotifier{clock: time.Unix(1000, 0)}
	f.desktopNotifier = &desktopNotifier{
		channel: ch,
		write:   func(s string) { f.out = append(f.out, s) },
		now:     func() time.Time { return f.clock },
	}
	return f
}

func TestNewDesktopNotifierDisabled(t *testing.T) {
	require.Nil(t, newDesktopNotifier(envFrom(map[string]string{"TERM": "xterm"}), func(string) {}))
	require.Nil(t, newDesktopNotifier(envFrom(map[string]string{notifyEnvVar: "off", "TERM": "xterm-kitty"}), func(string) {}))
	n := newDesktopNotifier(envFrom(map[string]string{"TERM": "xterm-kitty", "TMUX": "x"}), func(string) {})
	require.NotNil(t, n)
	require.Equal(t, notifyKitty, n.channel)
	require.Equal(t, muxTmux, n.mux)
}

func TestNotifierFocusPolicy(t *testing.T) {
	f := newFakeNotifier(notifyBell)

	// Focus unknown: needs-the-user notifications fire, nice-to-knows don't.
	require.True(t, f.send(notifyUnlessFocused, "Dagger", "one"))
	require.False(t, f.send(notifyIfBlurred, "Dagger", "two"))

	f.focus = focusIn
	require.False(t, f.send(notifyUnlessFocused, "Dagger", "three"))
	require.False(t, f.send(notifyIfBlurred, "Dagger", "four"))

	f.focus = focusOut
	require.True(t, f.send(notifyUnlessFocused, "Dagger", "five"))
	require.True(t, f.send(notifyIfBlurred, "Dagger", "six"))
	require.Equal(t, []string{"\a", "\a", "\a"}, f.out)
}

func TestNotifierDebounce(t *testing.T) {
	f := newFakeNotifier(notifyITerm2)
	require.True(t, f.send(notifyUnlessFocused, "Dagger", "same"))
	require.False(t, f.send(notifyUnlessFocused, "Dagger", "same"), "duplicate within the window")
	require.True(t, f.send(notifyUnlessFocused, "Dagger", "different"))
	require.True(t, f.send(notifyUnlessFocused, "Dagger", "same"), "not a repeat of the last one")
	f.clock = f.clock.Add(notifyDebounce / 2)
	require.False(t, f.send(notifyUnlessFocused, "Dagger", "same"))
	f.clock = f.clock.Add(notifyDebounce)
	require.True(t, f.send(notifyUnlessFocused, "Dagger", "same"), "window elapsed")
	require.Len(t, f.out, 4)
}

func TestFocusReportingTerminal(t *testing.T) {
	inner := tuist.NewHeadlessTerminal(80, 10)
	term := &focusReportingTerminal{Terminal: inner}
	require.NoError(t, term.Start(func([]byte) {}, func() {}))
	require.Equal(t, ansi.SetModeFocusEvent, inner.Output())
	inner.Reset()
	term.Stop()
	require.Equal(t, ansi.ResetModeFocusEvent, inner.Output())
}

func TestWithDesktopNotificationsSkipsFakeTerminals(t *testing.T) {
	inner := tuist.NewHeadlessTerminal(80, 10)
	term, n := withDesktopNotifications(inner, false)
	require.Same(t, tuist.Terminal(inner), term)
	require.Nil(t, n)
}

// notifyTestFrontend is a headless frontend with a fake notifier installed,
// as a real terminal would have.
func notifyTestFrontend(t *testing.T, db *dagui.DB) (*frontendPretty, *fakeNotifier) {
	t.Helper()
	t.Setenv("NO_COLOR", "1")
	fe := newWithTerminal(io.Discard, db, tuist.NewHeadlessTerminal(120, 10))
	fe.setupTUI()
	f := newFakeNotifier(notifyITerm2)
	fe.notifier = f.desktopNotifier
	return fe, f
}

func TestTerminalFocusListener(t *testing.T) {
	fe, f := notifyTestFrontend(t, dagui.NewDB())
	ctx := tuist.Context{}
	require.True(t, fe.handleTerminalFocus(ctx, uv.BlurEvent{}), "focus reports are consumed")
	require.Equal(t, focusOut, f.focus)
	require.True(t, fe.handleTerminalFocus(ctx, uv.FocusEvent{}))
	require.Equal(t, focusIn, f.focus)
	f.focus = focusOut
	require.False(t, fe.handleTerminalFocus(ctx, uv.KeyPressEvent(uv.Key{Code: 'x', Text: "x"})), "keys still reach the UI")
	require.Equal(t, focusIn, f.focus, "typing means focused")

	// Without a notifier, reports are still swallowed rather than dispatched.
	fe.notifier = nil
	require.True(t, fe.handleTerminalFocus(ctx, uv.FocusEvent{}))
}

func TestHandlePromptNotifies(t *testing.T) {
	fe, f := notifyTestFrontend(t, dagui.NewDB())
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the prompt returns at once; its notification is already queued
	var ok bool
	require.ErrorIs(t, fe.HandlePrompt(ctx, "", "Allow force-push to origin @ main?", &ok), context.Canceled)
	fe.tui.Step()
	require.Equal(t, []string{"\x1b]9;Dagger: Allow force-push to origin @ main?\a"}, f.out)

	var s string
	f.clock = f.clock.Add(time.Minute)
	require.ErrorIs(t, fe.HandlePrompt(ctx, "Name", "What should we call it?", &s), context.Canceled)
	fe.tui.Step()
	require.Len(t, f.out, 2, "a string prompt notifies once, not again through HandleForm")
	require.Equal(t, "\x1b]9;Dagger: Name: What should we call it?\a", f.out[1])

	require.ErrorIs(t, fe.HandleForm(ctx, NewForm()), context.Canceled)
	fe.tui.Step()
	require.Len(t, f.out, 3)
	require.Equal(t, "\x1b]9;Dagger: Input needed\a", f.out[2])
}

func TestHandlePromptQuietWhenFocusedOrReporting(t *testing.T) {
	fe, f := notifyTestFrontend(t, dagui.NewDB())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var ok bool

	f.focus = focusIn
	_ = fe.HandlePrompt(ctx, "", "Proceed?", &ok)
	fe.tui.Step()
	require.Empty(t, f.out, "the user is looking at it")

	f.focus = focusUnknown
	fe.traceID = "replay"
	_ = fe.HandlePrompt(ctx, "", "Proceed?", &ok)
	fe.tui.Step()
	require.Empty(t, f.out, "a trace replay never notifies")
}

func TestNotifyApproval(t *testing.T) {
	require.Equal(t, "scout needs approval: run rm -rf build?", approvalNotifyBody(" scout ", "run rm -rf build?"))
	require.Equal(t, "Dagger needs approval", approvalNotifyBody("", " "))

	fe, f := notifyTestFrontend(t, dagui.NewDB())
	fe.notifyApproval("scout", "push to main?")
	fe.tui.Step()
	require.Equal(t, []string{"\x1b]9;Dagger: scout needs approval: push to main?\a"}, f.out)
}
