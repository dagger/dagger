package idtui

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
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
	if !theme.Focused.FocusedButton.GetBold() || !theme.Focused.BlurredButton.GetBold() {
		t.Fatal("confirmation choices are not strongly styled")
	}
	if strings.Contains(theme.Blurred.MultiSelectSelector.String(), ">") {
		t.Fatal("blurred multi-select still has a selection caret")
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
	if got := field.View(); !strings.Contains(got, "▶ [ Install selected ]") || strings.Contains(got, "▶ [ Skip ]") {
		t.Fatalf("affirmative selection is not explicit:\n%s", got)
	}

	selected = false
	if got := field.View(); !strings.Contains(got, "▶ [ Skip ]") || strings.Contains(got, "▶ [ Install selected ]") {
		t.Fatalf("negative selection is not explicit:\n%s", got)
	}
	field.Blur()
	if got := field.View(); strings.Contains(got, "▶") {
		t.Fatalf("blurred confirmation retained its selection caret:\n%s", got)
	}

	var output bytes.Buffer
	if err := field.RunAccessible(&output, strings.NewReader("n\n")); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); !strings.Contains(got, "Install selected") || !strings.Contains(got, "Skip") {
		t.Fatalf("accessible prompt does not name both actions:\n%s", got)
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
