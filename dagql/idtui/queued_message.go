package idtui

import (
	"io"

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

func (q *QueuedMessageLabel) Render(ctx tuist.Context) tuist.RenderResult {
	if q.message == "" {
		return tuist.RenderResult{}
	}
	out := termenv.NewOutput(io.Discard, termenv.WithProfile(q.profile))
	// Show the queued message in gray/dim to indicate it's pending
	line := out.String("⏳ " + q.message).Foreground(termenv.ANSIBrightBlack).String()
	return tuist.RenderResult{
		Lines: []string{line},
	}
}
