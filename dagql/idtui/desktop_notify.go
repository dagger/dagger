package idtui

import (
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/vito/tuist"
)

// Desktop notifications: the TUI asks the terminal emulator to raise an OS
// notification when something needs the user -- a prompt or approval is
// waiting, or the agent they are driving finished its turn -- the way Claude
// Code does. Notifications are terminal escape sequences, so they only work on
// a real interactive terminal, never in report, plain, console or trace mode.
//
// DAGGER_TUI_NOTIFY picks the channel:
//
//	auto (default)    detect the terminal from the environment (see
//	                  autoNotifyChannel); unknown terminals get nothing
//	iterm2            OSC 9 (iTerm2, WezTerm, ...)
//	iterm2_with_bell  OSC 9 followed by BEL
//	kitty             OSC 99
//	ghostty           OSC 777 (Ghostty, and other rxvt-style notify support)
//	bell              BEL only (alias: terminal_bell)
//	off               never notify (aliases: none, disabled,
//	                  notifications_disabled, 0, false)
//
// Inside tmux (TMUX set) OSC sequences are wrapped in tmux's DCS passthrough,
// which needs `set -g allow-passthrough on` (tmux >= 3.3); inside GNU screen
// (STY set) in screen's. BEL is never wrapped: multiplexers forward or flag it
// themselves.
//
// Focus: the TUI turns on focus reporting (DECSET 1004) and stays quiet
// while the terminal is known to be focused. Until a terminal reports focus
// either way it is assumed unfocused, so terminals without focus reporting
// still notify; any keypress, click or paste counts as focus.
const notifyEnvVar = "DAGGER_TUI_NOTIFY"

// notifyTitle titles every notification: the body says what happened.
const notifyTitle = "Dagger"

const (
	// notifyDebounce drops an identical notification repeated within it.
	notifyDebounce = 10 * time.Second

	maxNotifyTitleRunes = 64
	maxNotifyBodyRunes  = 200
)

type notifyChannel int

const (
	notifyOff notifyChannel = iota
	notifyITerm2
	notifyITerm2WithBell
	notifyKitty
	notifyGhostty
	notifyBell
)

func (c notifyChannel) String() string {
	switch c {
	case notifyITerm2:
		return "iterm2"
	case notifyITerm2WithBell:
		return "iterm2_with_bell"
	case notifyKitty:
		return "kitty"
	case notifyGhostty:
		return "ghostty"
	case notifyBell:
		return "bell"
	default:
		return "off"
	}
}

// notifyMux is the terminal multiplexer escape sequences must pass through.
type notifyMux int

const (
	muxNone notifyMux = iota
	muxTmux
	muxScreen
)

// detectNotifyChannel resolves DAGGER_TUI_NOTIFY. An unrecognized value falls
// back to auto-detection rather than silently disabling notifications.
func detectNotifyChannel(getenv func(string) string) notifyChannel {
	switch strings.ToLower(strings.TrimSpace(getenv(notifyEnvVar))) {
	case "iterm2", "osc9":
		return notifyITerm2
	case "iterm2_with_bell":
		return notifyITerm2WithBell
	case "kitty", "osc99":
		return notifyKitty
	case "ghostty", "osc777":
		return notifyGhostty
	case "bell", "terminal_bell":
		return notifyBell
	case "off", "none", "disabled", "notifications_disabled", "0", "false", "no":
		return notifyOff
	default:
		return autoNotifyChannel(getenv)
	}
}

// autoNotifyChannel guesses the channel from the environment. Signals the
// terminal sets for itself (TERM_PROGRAM, TERM) come first; variables a
// multiplexer inherits from the terminal it was started in (LC_TERMINAL,
// KITTY_WINDOW_ID, GHOSTTY_RESOURCES_DIR) are the fallback, since tmux
// overwrites TERM_PROGRAM and TERM. An unknown terminal gets no notifications
// at all, as in Claude Code: an unrequested bell is a beep, not a
// notification. DAGGER_TUI_NOTIFY=bell opts in.
func autoNotifyChannel(getenv func(string) string) notifyChannel {
	term := getenv("TERM")
	program := getenv("TERM_PROGRAM")
	switch {
	case term == "dumb":
		return notifyOff
	case program == "iTerm.app":
		return notifyITerm2
	case strings.EqualFold(program, "ghostty"), term == "xterm-ghostty":
		return notifyGhostty
	case term == "xterm-kitty":
		return notifyKitty
	case program == "WezTerm":
		return notifyITerm2 // WezTerm implements OSC 9
	case program == "Apple_Terminal":
		return notifyBell // Terminal.app has no notification escape
	case getenv("LC_TERMINAL") == "iTerm2":
		return notifyITerm2
	case getenv("KITTY_WINDOW_ID") != "":
		return notifyKitty
	case getenv("GHOSTTY_RESOURCES_DIR") != "":
		return notifyGhostty
	default:
		return notifyOff
	}
}

func detectNotifyMux(getenv func(string) string) notifyMux {
	switch {
	case getenv("TMUX") != "":
		return muxTmux
	case getenv("STY") != "":
		return muxScreen
	default:
		return muxNone
	}
}

// sanitizeNotifyText makes text safe to embed in an OSC payload: control and
// format characters (which could end the sequence early, or reorder text with
// bidi overrides) are dropped, whitespace runs collapse to one space, and the
// result is capped at limit runes. Titles and bodies carry agent and module
// names and prompt text, none of which this package controls.
func sanitizeNotifyText(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	out := make([]rune, 0, min(len(s), limit+1))
	space := false
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			space = len(out) > 0
			continue
		case r == unicode.ReplacementChar, unicode.IsControl(r), unicode.Is(unicode.Cf, r):
			continue
		}
		if space {
			out = append(out, ' ')
			space = false
		}
		out = append(out, r)
		if len(out) > limit {
			// One past the cap: room for the ellipsis is all that is needed.
			trimmed := strings.TrimRight(string(out[:limit-1]), " ")
			return trimmed + "…"
		}
	}
	return string(out)
}

// encodeNotification renders a notification for a channel, including any
// multiplexer passthrough. id distinguishes kitty notifications, whose title
// and body arrive as separate chunks of one notification.
func encodeNotification(ch notifyChannel, mux notifyMux, id int, title, body string) string {
	title = sanitizeNotifyText(title, maxNotifyTitleRunes)
	body = sanitizeNotifyText(body, maxNotifyBodyRunes)
	if title == "" {
		title, body = body, ""
	}
	if title == "" {
		return ""
	}
	const (
		bel = "\a"
		st  = "\x1b\\"
	)
	var seqs []string
	trailer := ""
	switch ch {
	case notifyITerm2, notifyITerm2WithBell:
		// OSC 9 has a single message. A payload starting with "<digits>;"
		// would be a ConEmu command instead (e.g. 9;4 is progress), which the
		// title prefix rules out.
		msg := title
		if body != "" {
			msg += ": " + body
		}
		seqs = append(seqs, "\x1b]9;"+msg+bel)
		if ch == notifyITerm2WithBell {
			trailer = bel
		}
	case notifyKitty:
		ident := fmt.Sprintf("i=dagger-%d", id)
		if body == "" {
			seqs = append(seqs, "\x1b]99;"+ident+";"+title+st)
		} else {
			// d=0: more chunks follow; the body chunk completes it.
			seqs = append(seqs,
				"\x1b]99;"+ident+":d=0;"+title+st,
				"\x1b]99;"+ident+":p=body;"+body+st)
		}
	case notifyGhostty:
		// Fields are ';'-separated: keep the title from splitting.
		seqs = append(seqs, "\x1b]777;notify;"+strings.ReplaceAll(title, ";", ",")+";"+body+bel)
	case notifyBell:
		trailer = bel
	default:
		return ""
	}
	var out strings.Builder
	for _, seq := range seqs {
		out.WriteString(wrapForMux(mux, seq))
	}
	out.WriteString(trailer)
	return out.String()
}

// wrapForMux wraps one escape sequence in a multiplexer's passthrough. tmux
// wants every ESC inside doubled. Screen's passthrough ends at the first ST,
// so an inner ST becomes BEL, which terminates OSC just as well.
func wrapForMux(mux notifyMux, seq string) string {
	switch mux {
	case muxTmux:
		return "\x1bPtmux;" + strings.ReplaceAll(seq, "\x1b", "\x1b\x1b") + "\x1b\\"
	case muxScreen:
		return "\x1bP" + strings.ReplaceAll(seq, "\x1b\\", "\a") + "\x1b\\"
	default:
		return seq
	}
}

// notifyWhen is a notification's focus policy.
type notifyWhen int

const (
	// notifyUnlessFocused fires unless the terminal is known to be focused:
	// for things that need the user (prompts, approvals, a finished turn).
	notifyUnlessFocused notifyWhen = iota
	// notifyIfBlurred fires only once the terminal has reported losing
	// focus: for things merely nice to know (a long run finishing), so a
	// terminal that cannot report focus is never pestered with them.
	notifyIfBlurred
)

type terminalFocus int

const (
	focusUnknown terminalFocus = iota
	focusIn
	focusOut
)

// desktopNotifier writes notifications to the terminal. It is owned by the
// UI goroutine: every method must run there.
type desktopNotifier struct {
	channel notifyChannel
	mux     notifyMux
	write   func(string)
	now     func() time.Time

	focus terminalFocus

	seq      int
	lastText string
	lastAt   time.Time
}

// newDesktopNotifier returns nil when the environment disables
// notifications.
func newDesktopNotifier(getenv func(string) string, write func(string)) *desktopNotifier {
	ch := detectNotifyChannel(getenv)
	if ch == notifyOff {
		return nil
	}
	return &desktopNotifier{
		channel: ch,
		mux:     detectNotifyMux(getenv),
		write:   write,
		now:     time.Now,
	}
}

// send writes one notification, reporting whether it did.
func (n *desktopNotifier) send(when notifyWhen, title, body string) bool {
	if n == nil || n.write == nil {
		return false
	}
	switch when {
	case notifyUnlessFocused:
		if n.focus == focusIn {
			return false
		}
	case notifyIfBlurred:
		if n.focus != focusOut {
			return false
		}
	}
	now := n.now()
	key := title + "\x00" + body
	if key == n.lastText && now.Sub(n.lastAt) < notifyDebounce {
		return false
	}
	n.seq++
	seq := encodeNotification(n.channel, n.mux, n.seq, title, body)
	if seq == "" {
		return false
	}
	n.lastText, n.lastAt = key, now
	n.write(seq)
	return true
}

// focusReportingTerminal turns on terminal focus reporting for as long as
// the TUI owns the terminal: Exec stops and restarts it, so a command run in
// the foreground never receives focus reports meant for the TUI.
type focusReportingTerminal struct {
	tuist.Terminal
}

func (t *focusReportingTerminal) Start(onInput func([]byte), onResize func()) error {
	if err := t.Terminal.Start(onInput, onResize); err != nil {
		return err
	}
	t.Terminal.WriteString(ansi.SetModeFocusEvent)
	return nil
}

func (t *focusReportingTerminal) Stop() {
	t.Terminal.WriteString(ansi.ResetModeFocusEvent)
	t.Terminal.Stop()
}

// withDesktopNotifications sets up notifications for a frontend's terminal:
// only a real terminal attached to a TTY gets them. It returns the terminal
// to render to (wrapped to report focus) and the notifier, nil when disabled.
func withDesktopNotifications(term tuist.Terminal, realTerminal bool) (tuist.Terminal, *desktopNotifier) {
	if !realTerminal {
		return term, nil
	}
	if _, out := findTTYs(); out == nil {
		return term, nil
	}
	wrapped := &focusReportingTerminal{Terminal: term}
	n := newDesktopNotifier(os.Getenv, wrapped.WriteString)
	if n == nil {
		return term, nil
	}
	return wrapped, n
}

// handleTerminalFocus tracks the terminal's focus from its focus reports,
// and from input, which only a focused terminal receives. Runs on Tuist's
// event loop ahead of input dispatch; focus reports are consumed.
func (fe *frontendPretty) handleTerminalFocus(_ tuist.Context, ev uv.Event) bool {
	switch ev.(type) {
	case uv.FocusEvent:
		if fe.notifier != nil {
			fe.notifier.focus = focusIn
		}
		return true
	case uv.BlurEvent:
		if fe.notifier != nil {
			fe.notifier.focus = focusOut
		}
		return true
	case uv.KeyPressEvent, uv.MouseClickEvent, uv.PasteEvent:
		if fe.notifier != nil {
			fe.notifier.focus = focusIn
		}
	}
	return false
}

// notificationsEnabled reports whether this frontend may notify at all.
func (fe *frontendPretty) notificationsEnabled() bool {
	return fe.notifier != nil && !fe.reportOnly && fe.console == "" && fe.traceID == ""
}

// notify raises a desktop notification for something that needs the user,
// unless the terminal is focused or the same one just fired. Safe to call
// from any goroutine.
func (fe *frontendPretty) notify(title, body string) {
	if !fe.notificationsEnabled() {
		return
	}
	fe.dispatch(func() {
		fe.sendNotification(notifyUnlessFocused, title, body)
	})
}

// notifyApproval announces a pending approval: "<requester> needs approval:
// <prompt>". requester is the agent or module asking; empty reads "Dagger".
// Safe to call from any goroutine.
func (fe *frontendPretty) notifyApproval(requester, prompt string) {
	fe.notify(notifyTitle, approvalNotifyBody(requester, prompt))
}

func approvalNotifyBody(requester, prompt string) string {
	requester = strings.TrimSpace(requester)
	if requester == "" {
		requester = notifyTitle
	}
	body := requester + " needs approval"
	if prompt = strings.TrimSpace(prompt); prompt != "" {
		body += ": " + prompt
	}
	return body
}

// notifyPrompt announces a prompt or form waiting for an answer. Safe to call
// from any goroutine.
func (fe *frontendPretty) notifyPrompt(title, prompt string) {
	fe.notify(notifyTitle, promptNotifyBody(title, prompt))
}

func promptNotifyBody(title, prompt string) string {
	title, prompt = strings.TrimSpace(title), strings.TrimSpace(prompt)
	switch {
	case title != "" && prompt != "":
		return title + ": " + prompt
	case title != "":
		return title
	case prompt != "":
		return prompt
	default:
		return "Input needed"
	}
}

// sendNotification is notify for the UI goroutine.
func (fe *frontendPretty) sendNotification(when notifyWhen, title, body string) bool {
	if !fe.notificationsEnabled() {
		return false
	}
	return fe.notifier.send(when, title, body)
}
