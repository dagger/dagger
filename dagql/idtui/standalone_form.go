package idtui

import (
	"context"

	"github.com/charmbracelet/huh"
)

// RunStandaloneForm runs a huh form directly on the terminal, outside a running
// frontend, applying the same theme and keymap the TUI frontend uses so
// standalone prompts (from plain cobra commands) look identical to in-TUI ones
// like the `dagger setup` Cloud login prompt.
func RunStandaloneForm(ctx context.Context, form *huh.Form) error {
	return form.
		WithTheme(frontendFormTheme()).
		WithKeyMap(frontendFormKeyMap()).
		RunWithContext(ctx)
}
