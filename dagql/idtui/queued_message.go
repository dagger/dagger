package idtui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/vito/tuist"
)

// QueuedMessageLabel displays a queued message (submitted while the shell is
// busy) in gray above the prompt input. When there is no queued message it
// renders zero lines.
type QueuedMessageLabel struct {
	tuist.Compo
	message string
	profile termenv.Profile
}

// NewQueuedMessageLabel creates a new QueuedMessageLabel.
func NewQueuedMessageLabel(profile termenv.Profile) *QueuedMessageLabel {
	return &QueuedMessageLabel{profile: profile}
}

// SetMessage sets the queued message to display. Pass "" to clear.
func (q *QueuedMessageLabel) SetMessage(msg string) {
	q.message = msg
	q.Update()
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
