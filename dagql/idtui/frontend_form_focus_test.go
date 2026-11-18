package idtui

import (
	"context"
	"testing"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/stretchr/testify/require"
	"github.com/vito/tuist"
)

// Prompt forms (the /model picker, permission prompts, ...) borrow keyboard
// focus through a scoped tuist.FocusHandle. The contract under test: when the
// form goes away, focus RETURNS to whoever owned it before — for a form popped
// while typing at the prompt, that is the editline.

// TestPromptFormRestoresEditlineFocus reproduces the /model flow: the prompt
// owns focus, a picker form is presented, the user selects an entry, and the
// prompt must own focus again once the form is gone. This exercises
// presentPromptForm's child reshuffle, which removes the promptFrame (the
// focused TextInput's ancestor) before re-adding it — an operation that clears
// tuist focus, so the form's focus scope must be captured before it.
func TestPromptFormRestoresEditlineFocus(t *testing.T) {
	handler := &focusShellHandler{}
	fe := focusTestFrontend(t, dagui.NewDB(), handler)

	require.True(t, fe.tui.IsFocused(fe.textInput),
		"precondition: the prompt editline owns focus")

	var selected string
	form := NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("Pick a model").
			Options(
				huh.NewOption("model-a", "model-a"),
				huh.NewOption("model-b", "model-b"),
			).
			Value(&selected),
	))
	fe.handlePromptForm(form, nil)
	fe.tui.Step()

	require.True(t, fe.formFocused(), "the presented form owns focus")

	// Select the highlighted option; the single-field form completes and
	// tears down through the real quit path. The wrapped bubbletea model
	// delivers its quit asynchronously, so pump frames until teardown lands.
	fe.tui.Inject(tuist.ParseKey("enter"))
	awaitFormTeardown(t, fe)

	require.Equal(t, "model-a", selected)
	require.True(t, fe.tui.IsFocused(fe.textInput),
		"focus must return to the prompt editline after the form goes away")
}

// awaitFormTeardown Steps the TUI until the active form is gone. The teav1
// wrapper executes tea.Quit on a goroutine and dispatches the resulting
// QuitMsg back to the UI loop, so teardown lands a frame or two later.
func awaitFormTeardown(t *testing.T, fe *frontendPretty) {
	t.Helper()
	require.Eventually(t, func() bool {
		fe.tui.Step()
		return fe.activeForm == nil
	}, 5*time.Second, 10*time.Millisecond, "the form should tear down")
}

// TestPromptFormAbortRestoresEditlineFocus covers Ctrl+C while a form owns
// focus: only the form is cancelled, and the session's prompt remains usable.
func TestPromptFormAbortRestoresEditlineFocus(t *testing.T) {
	for _, kind := range []string{"picker", "password", "confirmation"} {
		t.Run(kind, func(t *testing.T) {
			handler := &focusShellHandler{}
			fe := focusTestFrontend(t, dagui.NewDB(), handler)
			fe.runCtx, fe.interrupt = context.WithCancelCause(context.Background())
			t.Cleanup(func() { fe.interrupt(nil) })

			var field huh.Field
			switch kind {
			case "picker":
				field = huh.NewSelect[string]().Title("Pick a model").Options(huh.NewOption("model-a", "model-a"))
			case "password":
				field = huh.NewInput().Title("API key").EchoMode(huh.EchoModePassword)
			case "confirmation":
				var confirmed bool
				field = NewExplicitConfirm("Delete", "Cancel", &confirmed)
			}
			form := NewForm(huh.NewGroup(field))
			fe.handlePromptForm(form, nil)
			fe.tui.Step()
			require.True(t, fe.formFocused(), "the presented form owns focus")
			req := fe.activeForm.request

			fe.tui.Inject(tuist.ParseKey("ctrl+c"))
			awaitFormTeardown(t, fe)

			require.NoError(t, fe.runCtx.Err(), "aborting a form must not cancel the frontend session")
			require.Zero(t, handler.interruptCount(), "aborting a form must not interrupt the agent")
			require.ErrorIs(t, req.err, huh.ErrUserAborted)
			require.ErrorIs(t, req.err, ErrInterrupted, "callers may still propagate interruption")
			require.True(t, req.finished)
			require.True(t, fe.tui.IsFocused(fe.textInput),
				"focus must return to the prompt editline after the form is aborted")

			// Editing and global Ctrl+C work normally again after the form closes.
			fe.tui.Inject(tuist.ParseKey("x"))
			fe.tui.Step()
			require.Equal(t, "x", fe.textInput.Value())
			fe.tui.Inject(tuist.ParseKey("ctrl+c"))
			fe.tui.Step()
			require.Equal(t, 1, handler.interruptCount())
			require.Empty(t, fe.textInput.Value())
		})
	}
}

func TestPromptFormAbortPreservesQueuedForm(t *testing.T) {
	fe := focusTestFrontend(t, dagui.NewDB(), &focusShellHandler{})
	fe.runCtx, fe.interrupt = context.WithCancelCause(context.Background())
	t.Cleanup(func() { fe.interrupt(nil) })
	first := fe.newPromptFormRequest(fe.runCtx, NewForm(huh.NewGroup(huh.NewInput().Title("First"))), nil)
	second := fe.newPromptFormRequest(fe.runCtx, NewForm(huh.NewGroup(huh.NewInput().Title("Second"))), nil)
	fe.enqueuePromptForm(first)
	fe.enqueuePromptForm(second)
	fe.tui.Step()

	fe.tui.Inject(tuist.ParseKey("ctrl+c"))
	require.Eventually(t, func() bool {
		fe.tui.Step()
		return fe.activeForm != nil && fe.activeForm.request == second
	}, 5*time.Second, 10*time.Millisecond)
	require.ErrorIs(t, first.err, huh.ErrUserAborted)
	require.False(t, second.finished)
	require.NoError(t, fe.runCtx.Err())
	require.True(t, fe.formFocused())

	fe.tui.Inject(tuist.ParseKey("ctrl+c"))
	awaitFormTeardown(t, fe)
	require.ErrorIs(t, second.err, huh.ErrUserAborted)
	require.NoError(t, fe.runCtx.Err())
	require.True(t, fe.tui.IsFocused(fe.textInput))
}
