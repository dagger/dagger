package idtui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/vito/tuist"
)

// ErrorLabel is a simple component that displays an error message.
// When the error is nil, it renders nothing (zero lines).
// Long error messages are word-wrapped to fit the available width.
type ErrorLabel struct {
	tuist.Compo
	err error
}

// NewErrorLabel creates a new ErrorLabel.
func NewErrorLabel() *ErrorLabel {
	return &ErrorLabel{}
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
	style := lipgloss.NewStyle().
		Foreground(lipgloss.Red).
		Width(ctx.Width)
	rendered := style.Render("Error: " + e.err.Error())
	return tuist.RenderResult{
		Lines: strings.Split(rendered, "\n"),
	}
}
