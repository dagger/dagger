package idtui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/vito/tuist"
)

// QueuedMessageLabel displays a queued message in gray above the prompt
// input: one held client-side behind a busy serial turn (recallable with
// alt+up), or one already sent to the focused agent mid-turn (an interject,
// absorbed at the agent's next step boundary -- on the record, so not
// recallable). When there is no queued message it renders zero lines.
type QueuedMessageLabel struct {
	tuist.Compo
	message string
	sent    bool
	profile termenv.Profile
}

// NewQueuedMessageLabel creates a new QueuedMessageLabel.
func NewQueuedMessageLabel(profile termenv.Profile) *QueuedMessageLabel {
	return &QueuedMessageLabel{profile: profile}
}

// SetMessage sets a RECALLABLE queued message to display -- one still held
// client-side, waiting for a serial turn to finish. Pass "" to clear.
func (q *QueuedMessageLabel) SetMessage(msg string) {
	q.message = msg
	q.sent = false
	q.Update()
}

// SetSentMessage displays a message that was already handed to the engine (a
// mid-turn interject): it is on the record and will be absorbed at the
// agent's next step boundary, so it shows as queued but cannot be recalled
// for editing.
func (q *QueuedMessageLabel) SetSentMessage(msg string) {
	q.message = msg
	q.sent = true
	q.Update()
}

// Sent reports whether the displayed message was already sent to the engine.
func (q *QueuedMessageLabel) Sent() bool {
	return q.sent
}

// Message returns the current queued message.
func (q *QueuedMessageLabel) Message() string {
	return q.message
}

func (q *QueuedMessageLabel) Render(ctx tuist.Context) {
	if q.message == "" {
		return
	}
	// Collapse newlines so a multi-line interject stays a single status line,
	// keeping height accounting (queuedMessageHeight) exact.
	oneLine := strings.Join(strings.Fields(q.message), " ")
	out := NewOutput(new(strings.Builder), termenv.WithProfile(q.profile))
	line := out.String("⏳ " + oneLine).Foreground(termenv.ANSIBrightBlack).Faint().String()
	if ctx.Width > 0 {
		line = ansi.Truncate(line, ctx.Width, "…")
	}
	ctx.Lines(line)
}
