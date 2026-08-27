package idtui

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/vito/tuist"
)

func TestFrontendFormThemeUsesStructuralFocusMarkers(t *testing.T) {
	theme := frontendFormTheme()
	if _, ok := theme.Focused.FocusedButton.GetBackground().(lipgloss.NoColor); !ok {
		t.Fatal("focused button still has a background")
	}
	if _, ok := theme.Focused.BlurredButton.GetBackground().(lipgloss.NoColor); !ok {
		t.Fatal("inactive button still has a background")
	}
	if _, ok := theme.Blurred.FocusedButton.GetBackground().(lipgloss.NoColor); !ok {
		t.Fatal("button in an unfocused confirmation still has a background")
	}
	if theme.Focused.Base.GetBorderLeft() {
		t.Fatal("focused form field still has a left border")
	}
	if theme.Blurred.Base.GetBorderLeft() {
		t.Fatal("blurred form field still reserves a left border")
	}
	if theme.Focused.Base.GetPaddingLeft() != 0 || theme.Blurred.Base.GetPaddingLeft() != 0 {
		t.Fatal("form fields still reserve a leading padding column")
	}
	if !theme.Focused.FocusedButton.GetBold() || theme.Focused.BlurredButton.GetBold() {
		t.Fatal("only the focused confirmation choice should be bold")
	}
	if strings.Contains(theme.Blurred.MultiSelectSelector.String(), ">") {
		t.Fatal("blurred multi-select still has a selection caret")
	}
	if got := theme.Focused.MultiSelectSelector.String(); got != "▶ " {
		t.Fatalf("focused multi-select selector = %q, want %q", got, "▶ ")
	}
}

func TestFrontendFormKeymapNamesSpaceToggle(t *testing.T) {
	keymap := frontendFormKeyMap()
	if got := keymap.MultiSelect.Toggle.Help().Key; got != "space" {
		t.Fatalf("multi-select toggle key = %q, want space", got)
	}
}

func TestAbortedFormInterrupts(t *testing.T) {
	form := huh.NewForm(huh.NewGroup(huh.NewConfirm()))
	form.State = huh.StateAborted
	err := formCompletionError(form)
	if !errors.Is(err, ErrInterrupted) || !errors.Is(err, huh.ErrUserAborted) {
		t.Fatalf("aborted form returned %v", err)
	}
}

func TestExplicitConfirmHasTextualSelectionAndAccessibleLabels(t *testing.T) {
	selected := true
	field := NewExplicitConfirm("Install selected", "Skip", &selected)
	field.WithTheme(frontendFormTheme())
	if got := field.View(); strings.Contains(got, "▶") {
		t.Fatalf("unfocused confirmation has a selection caret:\n%s", got)
	}

	field.Focus()
	if got := field.View(); !strings.Contains(got, "▶ Install selected") || strings.Contains(got, "▶ Skip") || strings.Contains(got, "[") {
		t.Fatalf("affirmative selection is not explicit:\n%s", got)
	}

	selected = false
	if got := field.View(); !strings.Contains(got, "▶ Skip") || strings.Contains(got, "▶ Install selected") || strings.Contains(got, "[") {
		t.Fatalf("negative selection is not explicit:\n%s", got)
	}
	field.Blur()
	if got := field.View(); strings.Contains(got, "▶") {
		t.Fatalf("blurred confirmation retained its selection caret:\n%s", got)
	}
	var help strings.Builder
	for _, binding := range field.KeyBinds() {
		help.WriteString(binding.Help().Key)
		help.WriteString(binding.Help().Desc)
	}
	if got := help.String(); strings.ContainsAny(got, "[]▶") || !strings.Contains(got, "Install selected") || !strings.Contains(got, "Skip") {
		t.Fatalf("confirmation keymap labels are not semantic: %q", got)
	}
	if strings.Contains(help.String(), "back") || strings.Contains(help.String(), "next") {
		t.Fatalf("confirmation keymap includes redundant field navigation: %q", help.String())
	}

	var output bytes.Buffer
	if err := field.RunAccessible(&output, strings.NewReader("n\n")); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); !strings.Contains(got, "Install selected") || !strings.Contains(got, "Skip") {
		t.Fatalf("accessible prompt does not name both actions:\n%s", got)
	}
}

func TestExplicitConfirmTitleIsSubtleHeader(t *testing.T) {
	theme := frontendFormTheme()
	style := explicitConfirmTitleStyle(&theme.Focused)
	if !style.GetBold() || !style.GetItalic() {
		t.Fatal("confirmation title is not bold and italic")
	}
	if got := style.GetForeground(); got != lipgloss.Color("8") {
		t.Fatalf("confirmation title foreground = %v, want ANSI bright black", got)
	}
}

func TestExplicitConfirmTitleLink(t *testing.T) {
	selected := true
	field := NewExplicitConfirm("Yes", "No", &selected).
		Title("Log in to Dagger Cloud?").
		TitleLink("https://dagger.io/cloud")
	if got := field.View(); !strings.Contains(got, ansi.SetHyperlink("https://dagger.io/cloud")) {
		t.Fatalf("confirmation title is not linked: %q", got)
	}
}

func TestExplicitChoiceHasOneTextualSelection(t *testing.T) {
	choice := "login"
	field := NewExplicitChoice(&choice,
		huh.NewOption("Log in", "login"),
		huh.NewOption("Not now", "not-now"),
		huh.NewOption("Never ask again", "never"),
	)
	field.WithTheme(frontendFormTheme())
	field.WithKeyMap(huh.NewDefaultKeyMap())
	field.Focus()
	if got := field.View(); strings.Count(got, "▶") != 1 || !strings.Contains(got, "▶ Log in") {
		t.Fatalf("choice does not identify exactly one selection:\n%s", got)
	}

	field.Update(tea.KeyMsg{Type: tea.KeyRight})
	if got := field.View(); strings.Count(got, "▶") != 1 || !strings.Contains(got, "▶ Not now") {
		t.Fatalf("right did not move the explicit selection:\n%s", got)
	}
	field.Blur()
	if got := field.View(); strings.Contains(got, "▶") {
		t.Fatalf("blurred choice retained its selection caret:\n%s", got)
	}
}

func TestFormFieldsFlowWithVerticalKeys(t *testing.T) {
	downKeys := map[string]tea.KeyMsg{
		"down":   {Type: tea.KeyDown},
		"j":      {Type: tea.KeyRunes, Runes: []rune{'j'}},
		"ctrl+n": {Type: tea.KeyCtrlN},
	}
	for name, keyMsg := range downKeys {
		t.Run(name, func(t *testing.T) {
			var selected []string
			multi := NewFlowMultiSelect(
				huh.NewMultiSelect[string]().
					Options(
						huh.NewOption("one", "one"),
						huh.NewOption("two", "two"),
					).
					Value(&selected),
				"two",
			)
			multi.WithKeyMap(huh.NewDefaultKeyMap())
			multi.Focus()
			if _, cmd := multi.Update(keyMsg); cmd != nil {
				t.Fatalf("%s before the final option advanced to the next field", name)
			}
			if hovered, ok := multi.Hovered(); !ok || hovered != "two" {
				t.Fatalf("%s did not reach final option: %q, %v", name, hovered, ok)
			}
			if _, cmd := multi.Update(keyMsg); cmd == nil {
				t.Fatalf("%s on the final option did not advance to the next field", name)
			}
		})
	}

	upKeys := map[string]tea.KeyMsg{
		"up":     {Type: tea.KeyUp},
		"k":      {Type: tea.KeyRunes, Runes: []rune{'k'}},
		"ctrl+p": {Type: tea.KeyCtrlP},
	}
	for name, keyMsg := range upKeys {
		t.Run(name, func(t *testing.T) {
			install := true
			confirm := NewExplicitConfirm("Install selected", "Skip", &install)
			confirm.Focus()
			if _, cmd := confirm.Update(keyMsg); cmd == nil {
				t.Fatalf("%s from the confirmation did not return to the previous field", name)
			}
		})
	}
}

func TestMountedFormUpdatesKeymapAndUsesNaturalHeight(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	fe := NewWithDB(io.Discard, dagui.NewDB())
	fe.setupTUI()
	if before := strings.Join(fe.tui.RenderLines(), "\n"); !strings.Contains(before, "verbosity") {
		t.Fatalf("initial keymap did not render navigation keys:\n%s", before)
	}

	apply := true
	form := NewForm(huh.NewGroup(
		NewExplicitConfirm("Apply", "Discard", &apply).Title("Apply changes?"),
	))
	fe.window.Height = 40
	fe.handlePromptForm(form, func(*huh.Form) {})
	after := strings.Join(fe.tui.RenderLines(), "\n")
	if !strings.Contains(after, "toggle") || strings.Contains(after, "verbosity") {
		t.Fatalf("form keymap did not replace navigation keys:\n%s", after)
	}
	plainAfter := ansi.Strip(after)
	flushTitle := strings.HasPrefix(plainAfter, "Apply changes?") || strings.Contains(plainAfter, "\nApply changes?")
	if !flushTitle || strings.HasPrefix(plainAfter, " Apply changes?") || strings.Contains(plainAfter, "\n Apply changes?") {
		t.Fatalf("form retained a leading padding column:\n%s", plainAfter)
	}
	if height := strings.Count(fe.formModel.View(), "\n") + 1; height > 8 {
		t.Fatalf("compact confirmation reserved %d lines", height)
	}
}

type commandViewFixture struct {
	tuist.Compo
	final bool
	label string
	child tuist.Component
}

func (view *commandViewFixture) SetFinal(final bool) {
	view.final = final
	view.Update()
}

func (view *commandViewFixture) Render(ctx tuist.Context) {
	if view.final {
		ctx.Line("final " + view.label)
	} else {
		ctx.Line("live " + view.label)
	}
	if view.child != nil {
		view.RenderChild(ctx, view.child)
	}
}

func TestCommandViewOwnsLiveAndFinalRendering(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	fe := NewWithDB(io.Discard, dagui.NewDB())
	fe.reportOnly = true
	view := &commandViewFixture{label: "setup"}
	handle := fe.SetView(func(ViewContext) CommandView { return view })

	live := strings.Join(fe.tui.RenderLines(), "\n")
	if !strings.Contains(live, "live setup") {
		t.Fatalf("live render did not use command view:\n%s", live)
	}

	handle.Update(func() { view.label = "workspace" })
	var final bytes.Buffer
	if err := fe.FinalRender(&final); err != nil {
		t.Fatal(err)
	}
	if got := final.String(); !strings.Contains(got, "final workspace") {
		t.Fatalf("final render did not use updated command view:\n%s", got)
	}
}

func TestCommandViewInitializesPerRenderState(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	term := tuist.NewHeadlessTerminal(73, 19)
	fe := newWithTerminal(io.Discard, dagui.NewDB(), term)
	fe.reportOnly = true
	view := &commandViewFixture{label: "setup"}
	fe.SetView(func(ViewContext) CommandView { return view })

	claimed := prettyTestSpanID(1)
	fe.claims.claimErrorID(claimed)
	_ = fe.tui.Frame()

	if fe.claims.hasError(claimed) {
		t.Fatal("command view retained claims from a previous render")
	}
	if got, want := fe.window, (windowSize{Width: 73, Height: 19}); got != want {
		t.Fatalf("command view window = %+v, want %+v", got, want)
	}
}

func TestSpanListSelectsCommandOwnedRoots(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	db := dagui.NewDB()
	rootID := prettyTestSpanID(1)
	keepID := prettyTestSpanID(2)
	dropID := prettyTestSpanID(3)
	start := time.Unix(100, 0)
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{ID: rootID, TraceID: prettyTestTraceID(), Name: "setup", StartTime: start, EndTime: start.Add(time.Second), Final: true},
		{ID: keepID, TraceID: prettyTestTraceID(), Name: "keep install", ParentID: rootID, StartTime: start, EndTime: start.Add(time.Second), Final: true, Reveal: true},
		{ID: dropID, TraceID: prettyTestTraceID(), Name: "drop install", ParentID: rootID, StartTime: start, EndTime: start.Add(time.Second), Final: true, Reveal: true},
	})
	db.SetPrimarySpan(rootID)

	fe := NewWithDB(io.Discard, db)
	fe.reportOnly = true
	fe.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity
	view := &commandViewFixture{label: "setup"}
	var list *SpanListView
	fe.SetView(func(ctx ViewContext) CommandView {
		list = ctx.SpanList(
			func() dagui.SpanID { return rootID },
			func() []dagui.SpanID { return []dagui.SpanID{keepID} },
		)
		view.child = list
		return view
	})

	rendered := strings.Join(fe.tui.RenderLines(), "\n")
	if !strings.Contains(rendered, "keep install") {
		t.Fatalf("selected span was not rendered:\n%s", rendered)
	}
	if strings.Contains(rendered, "drop install") {
		t.Fatalf("unselected span was rendered:\n%s", rendered)
	}
	if !list.FocusFirst() || fe.FocusedSpan != keepID {
		t.Fatalf("span list did not focus selected root: got %s, want %s", fe.FocusedSpan, keepID)
	}
}
