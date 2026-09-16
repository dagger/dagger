package idtui

import (
	"context"
	"io"
	"os"

	"github.com/charmbracelet/huh"
	"golang.org/x/term"
)

// StandaloneFormInput is implemented by frontends whose terminal keeps the
// process's only stdin reader after the TUI stops (tuist's reader lives for
// the process lifetime and discards input once stopped). A standalone prompt
// reading os.Stdin directly would race that reader for every keystroke —
// losing roughly half of them — so the frontend hands out an exclusive
// passthrough reader instead. The returned restore func must be called when
// the prompt is done; it re-routes input and unblocks any reader the prompt's
// program left behind.
type StandaloneFormInput interface {
	StandaloneFormInput() (io.Reader, func(), bool)
}

// RunStandaloneForm runs a huh form directly on the terminal, outside a
// running frontend, applying the same theme and keymap the TUI frontend uses
// so standalone prompts (from plain cobra commands) look identical to in-TUI
// ones like the `dagger setup` Cloud login prompt.
//
// When fe's terminal has run (and stopped) in this process, input is routed
// through the frontend's passthrough reader and the tty is put back into raw
// mode for the duration of the form; otherwise the form reads os.Stdin
// directly.
func RunStandaloneForm(ctx context.Context, fe Frontend, form *huh.Form) error {
	form = form.
		WithTheme(frontendFormTheme()).
		WithKeyMap(frontendFormKeyMap())
	if sfi, ok := fe.(StandaloneFormInput); ok {
		if input, restore, active := sfi.StandaloneFormInput(); active {
			defer restore()
			// The terminal was restored to cooked mode when the TUI stopped;
			// the form needs per-key input on the shared tty.
			if fd := int(os.Stdin.Fd()); term.IsTerminal(fd) {
				if old, err := term.MakeRaw(fd); err == nil {
					defer term.Restore(fd, old) //nolint:errcheck
				}
			}
			return form.WithInput(input).RunWithContext(ctx)
		}
	}
	return form.RunWithContext(ctx)
}
