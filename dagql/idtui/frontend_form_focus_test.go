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

// TestPromptFormAbortRestoresEditlineFocus covers the abort path (Ctrl+C
// reaches Huh while a form owns focus): even though an aborted form also
// triggers the frontend-wide interrupt contract, the prompt must be focused
// again afterwards.
func TestPromptFormAbortRestoresEditlineFocus(t *testing.T) {
	handler := &focusShellHandler{}
	fe := focusTestFrontend(t, dagui.NewDB(), handler)

	// An aborted form triggers the frontend-wide interrupt contract; give the
	// headless harness the cancel func Run would normally install.
	fe.runCtx, fe.interrupt = context.WithCancelCause(context.Background())

	var selected string
	form := NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("Pick a model").
			Options(huh.NewOption("model-a", "model-a")).
			Value(&selected),
	))
	fe.handlePromptForm(form, nil)
	fe.tui.Step()
	require.True(t, fe.formFocused(), "the presented form owns focus")

	fe.tui.Inject(tuist.ParseKey("ctrl+c"))
	awaitFormTeardown(t, fe)

	require.True(t, fe.tui.IsFocused(fe.textInput),
		"focus must return to the prompt editline after the form is aborted")
}
