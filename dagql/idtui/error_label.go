package idtui

import (
	"io"

	"github.com/muesli/termenv"
	"github.com/vito/tuist"
)

// ErrorLabel is a simple component that displays an error message.
// When the error is nil, it renders nothing (zero lines).
type ErrorLabel struct {
	tuist.Compo
	err     error
	profile termenv.Profile
}

// NewErrorLabel creates a new ErrorLabel.
func NewErrorLabel(profile termenv.Profile) *ErrorLabel {
	return &ErrorLabel{profile: profile}
}

// SetError sets the error to display. Pass nil to clear it.
func (e *ErrorLabel) SetError(err error) {
	e.err = err
	e.Update()
}

func (e *ErrorLabel) Render(ctx tuist.Context) tuist.RenderResult {
	if e.err == nil {
		return tuist.RenderResult{}
	}
	out := termenv.NewOutput(io.Discard, termenv.WithProfile(e.profile))
	line := out.String("Error: " + e.err.Error()).Foreground(termenv.ANSIRed).String()
	return tuist.RenderResult{
		Lines: []string{line},
	}
}
