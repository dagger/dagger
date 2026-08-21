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

func TestFrontendFormThemeOnlyHighlightsFocusedButton(t *testing.T) {
	theme := frontendFormTheme()
	if _, ok := theme.Focused.FocusedButton.GetBackground().(lipgloss.NoColor); ok {
		t.Fatal("focused button has no background")
	}
	if _, ok := theme.Focused.BlurredButton.GetBackground().(lipgloss.NoColor); !ok {
		t.Fatal("inactive button still has a background")
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
