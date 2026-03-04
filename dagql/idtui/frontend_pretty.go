package idtui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/adrg/xdg"
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/cellbuf"
	"github.com/muesli/reflow/truncate"
	"github.com/muesli/termenv"
	"github.com/pkg/browser"
	"github.com/vito/bubbline/editline"
	"github.com/vito/bubbline/history"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"golang.org/x/term"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui/multiprefixw"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/util/cleanups"
	telemetry "github.com/dagger/otel-go"

	"codeberg.org/vito/tuist"
)

var historyFile = filepath.Join(xdg.DataHome, "dagger", "histfile")

var (
	ErrShellExited = errors.New("shell exited")
	ErrInterrupted = errors.New("interrupted")
)

// windowSize replaces tea.WindowSizeMsg for terminal dimensions.
type windowSize struct {
	Width, Height int
}

// backgroundRequest communicates Background calls to the main run loop.
type backgroundRequest struct {
	cmd  tea.ExecCommand
	raw  bool
	done chan error
}

type frontendPretty struct {
	tuist.Compo

	dagui.FrontendOpts

	dag *dagger.Client

	// don't show live progress; just print a full report at the end
	reportOnly bool

	// updated by Run
	tui         *tuist.TUI
	run         func(context.Context) (cleanups.CleanupF, error)
	runCtx      context.Context
	interrupt   context.CancelCauseFunc
	interrupted bool
	quitting    bool
	done        bool
	err         error
	cleanup     func()

	// lifecycle channels
	quit          chan struct{}
	backgroundReq chan backgroundRequest

	// updated by Shell
	shell           ShellHandler
	shellCtx        context.Context
	shellInterrupt  context.CancelCauseFunc
	promptFg        termenv.Color
	promptErr       error
	editline        *editline.Model
	editlineFocused bool
	autoModeSwitch  bool
	shellRunning    bool
	shellLock       sync.Mutex

	// updated as events are written
	db           *dagui.DB
	logs         *prettyLogs
	eof          bool
	backgrounded bool
	autoFocus    bool
	debugged     dagui.SpanID
	focusedIdx   int
	rowsView     *dagui.RowsView
	rows         *dagui.Rows
	pressedKey   string
	pressedKeyAt time.Time

	// set when authenticated to Cloud
	cloudURL string

	// TUI state/config
	spinner      *Rave
	profile      termenv.Profile
	window       windowSize // terminal dimensions
	contentWidth int
	browserBuf   *strings.Builder      // logs if browser fails
	finalRender  bool                  // whether we're doing the final render
	shownErrs    map[dagui.SpanID]bool // which errors we've rendered
	stdin        io.Reader             // used by backgroundMsg for running terminal
	writer       io.Writer

	// notification bubbles (overlay-based, replacing old sidebar)
	notifications   map[string]*NotificationBubble // keyed by section title
	overlayHandles  map[string]*tuist.OverlayHandle
	notificationOrder []string // ordered titles for stacking

	// messages to print before the final render
	msgPreFinalRender strings.Builder

	// Add prompt field
	form *huh.Form

	// track whether we've already spawned the run function
	spawned bool

	// per-span tree components for incremental rendering
	spanTrees           map[dagui.SpanID]*SpanTreeView
	topTrees            []*SpanTreeView // top-level tree views, ordered
	renderVersion       uint64          // bumped on global render config changes (verbosity, zoom)
	lastRenderedVersion uint64          // renderVersion at last Render, for detecting changes
}

// Verify interface compliance at compile time.
var (
	_ tuist.Component   = (*frontendPretty)(nil)
	_ tuist.Interactive = (*frontendPretty)(nil)
	_ tuist.Mounter     = (*frontendPretty)(nil)
)

// treePrefix holds pre-computed prefix strings for a SpanTreeView.
// These are set by the parent SpanTreeView when rendering its children.
// By computing prefixes top-down through the tree, we avoid the stale-prefix
// problem that occurred when each row walked up the TraceRow parent chain
// independently.
type treePrefix struct {
	// step is the prefix for the step title line (ancestor bars + connector).
	// e.g., "│ ├╴" for a non-last child at depth 2.
	step string
	// cont is the prefix for continuation lines (ancestor bars + bar/space).
	// e.g., "│ │ " for a non-last child at depth 2.
	cont string
	// forChildren is the accumulated ancestor bars to pass to this node's
	// children. Equal to cont (the parent's column continues for children).
	forChildren string
	// contWidth is the visual width of cont (for available width calculation).
	contWidth int
}

// SpanTreeView is a tuist component that renders a TraceTree node and its
// children recursively. This is the tree-based replacement for SpanRowView.
//
// The parent SpanTreeView computes and sets the prefix strings for each
// child before calling RenderChild. When a parent's status changes, it
// re-renders, recomputing child prefixes — so prefixes are always fresh.
//
// Children that haven't changed return cached results from RenderChild.
// The parent just concatenates cached child lines, which is O(pointers).
type SpanTreeView struct {
	tuist.Compo
	fe     *frontendPretty
	spanID dagui.SpanID

	// prefix holds the pre-computed indentation from ancestors.
	// Set by the parent before RenderChild is called.
	prefix treePrefix

	// children are the expanded child SpanTreeViews, ordered.
	children []*SpanTreeView
	// childMap indexes children by span ID for reuse across renders.
	childMap map[dagui.SpanID]*SpanTreeView

	// spinner is non-nil when the span is running; self-ticking.
	spinner *SpinnerView

	// childrenGapPrefix is the prefix for gap lines between this node's
	// children. It shows all ancestor bars + this node's own bar column.
	// Computed by syncTreeNode. Unlike a child's prefix.cont (which omits
	// the parent bar for last children), this always shows the parent bar.
	childrenGapPrefix string

	// focused tracks whether this span is the currently focused span.
	// Synced by syncSpanTreeState from fe.FocusedSpan.
	focused bool

	// Render metadata — set during Render() for focus-line lookup.
	// These are output-derived values, not input state that drives rendering.
	selfLineCount   int   // lines from self content (before children)
	childGapCounts  []int // gap line count before each child
	childLineCounts []int // total line count from each child's RenderChild
}

var _ tuist.Component = (*SpanTreeView)(nil)

// SpinnerView is a self-ticking spinner component. It starts a tick goroutine
// on mount (via OnMount/ctx.Done()) and stops when dismounted. Only mounted
// when a span is running, so completed spans have zero per-frame cost.
type SpinnerView struct {
	tuist.Compo
	rave *Rave
	now  time.Time
}

var (
	_ tuist.Component = (*SpinnerView)(nil)
	_ tuist.Mounter   = (*SpinnerView)(nil)
)

// OnMount starts a tick goroutine. ctx.Done() fires on dismount.
func (s *SpinnerView) OnMount(ctx tuist.EventContext) {
	go func() {
		ticker := time.NewTicker(33 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case t := <-ticker.C:
				ctx.Dispatch(func() {
					s.now = t
					s.Update()
				})
			}
		}
	}()
}

func (s *SpinnerView) Render(ctx tuist.RenderContext) tuist.RenderResult {
	return tuist.RenderResult{Lines: []string{s.rave.ViewFancy(s.now)}}
}

// ViewFancy returns the current spinner frame for inline use.
func (s *SpinnerView) ViewFancy() string {
	return s.rave.ViewFancy(s.now)
}

// Render produces the lines for this span tree node and its children.
// This method is stateless — all component state (prefix, children,
// focus, spinner) is synced by syncSpanTreeState() before Render runs.
func (s *SpanTreeView) Render(ctx tuist.RenderContext) tuist.RenderResult {
	row := s.fe.rows.BySpan[s.spanID]
	if row == nil {
		return tuist.RenderResult{Lines: ctx.Recycle}
	}

	// Render the spinner as a child so tuist manages its lifecycle
	// (OnMount starts the tick goroutine, dismount stops it).
	if s.spinner != nil {
		s.RenderChild(s.spinner, ctx)
	}

	buf := new(strings.Builder)
	out := NewOutput(buf, termenv.WithProfile(s.fe.profile))
	r := newRenderer(s.fe.db, s.fe.contentWidth/2, s.fe.FrontendOpts, false)

	// Override fancyIndent with our pre-computed prefix.
	r.indentFunc = s.indentFunc(out)

	s.fe.renderRowContent(out, r, row, "")

	text := buf.String()
	lines := ctx.Recycle
	if text != "" {
		lines = append(lines, strings.Split(strings.TrimSuffix(text, "\n"), "\n")...)
	}
	s.selfLineCount = len(lines)

	// Render children (already synced by syncSpanTreeState).
	s.childGapCounts = s.childGapCounts[:0]
	s.childLineCounts = s.childLineCounts[:0]
	for _, child := range s.children {
		// Gap line between children — uses parent's gap prefix (which always
		// shows the parent bar), not the child's prefix.cont (which omits
		// the parent bar for the last child).
		var gapCount int
		childRow := s.fe.rows.BySpan[child.spanID]
		if childRow != nil {
			gaps := s.fe.renderTreeGap(r, childRow, s.childrenGapPrefix)
			gapCount = len(gaps)
			lines = append(lines, gaps...)
		}

		childCtx := ctx
		childCtx.Width = ctx.Width - child.prefix.contWidth
		result := s.RenderChild(child, childCtx)
		lines = append(lines, result.Lines...)

		s.childGapCounts = append(s.childGapCounts, gapCount)
		s.childLineCounts = append(s.childLineCounts, len(result.Lines))
	}

	return tuist.RenderResult{Lines: lines}
}

// indentFunc returns a fancyIndent override that uses the pre-computed prefix.
// It only applies to the SpanTreeView's own span; other rows (e.g., synthetic
// rows from renderErrorCause) return false to fall through to the original
// fancyIndent which walks the row's parent chain.
func (s *SpanTreeView) indentFunc(out TermOutput) func(TermOutput, *dagui.TraceRow, bool, bool) bool {
	return func(o TermOutput, row *dagui.TraceRow, selfBar, selfHoriz bool) bool {
		// Only use tree prefix for our own span. Other rows (synthetic
		// rootCauseRows, etc.) need the original parent-chain walk.
		if row.Span.ID != s.spanID {
			return false
		}
		if selfHoriz {
			fmt.Fprint(o, s.prefix.step)
		} else if selfBar {
			fmt.Fprint(o, s.prefix.cont)
			// Also render self bar (for multi-line call args)
			span := row.Span
			color := restrainedStatusColor(span)
			var symbol string
			if row.ShowingChildren && !row.Span.Reveal {
				symbol = VertBar
			} else {
				symbol = " "
			}
			fmt.Fprint(o, out.String(symbol+" ").Foreground(color).Faint())
		} else {
			fmt.Fprint(o, s.prefix.cont)
		}
		return true
	}
}

// computeChildPrefix computes the prefix for a child at the given position.
func (s *SpanTreeView) computeChildPrefix(out TermOutput, hasNext bool) treePrefix {
	row := s.fe.rows.BySpan[s.spanID]
	if row == nil {
		return treePrefix{}
	}
	span := row.Span
	color := restrainedStatusColor(span)

	var connector, bar string
	if hasNext && !span.Reveal && len(span.RevealedSpans.Order) == 0 {
		connector = out.String(VertRightBar + HorizHalfLeftBar).Foreground(color).Faint().String()
		bar = out.String(VertBar + " ").Foreground(color).Faint().String()
	} else {
		connector = out.String(CornerBottomLeft + HorizHalfLeftBar).Foreground(color).Faint().String()
		bar = "  "
	}

	return treePrefix{
		step:        s.prefix.forChildren + connector,
		cont:        s.prefix.forChildren + bar,
		forChildren: s.prefix.forChildren + bar,
		contWidth:   s.prefix.contWidth + 2,
	}
}

// getOrCreateSpanTree returns the SpanTreeView for the given span ID,
// creating one if it doesn't exist.
func (fe *frontendPretty) getOrCreateSpanTree(spanID dagui.SpanID) *SpanTreeView {
	if fe.spanTrees == nil {
		fe.spanTrees = make(map[dagui.SpanID]*SpanTreeView)
	}
	st, ok := fe.spanTrees[spanID]
	if !ok {
		st = &SpanTreeView{
			fe:     fe,
			spanID: spanID,
		}
		fe.spanTrees[spanID] = st
	}
	// Sync spinner: mount when running, dismount when done
	fe.syncSpinnerTree(st)
	return st
}

// syncSpinnerTree sets or clears the spinner on a SpanTreeView based on
// whether the span is currently running. The spinner's lifecycle is
// managed by RenderChild in SpanTreeView.Render — tuist auto-mounts it
// (firing OnMount to start the tick goroutine) and auto-dismounts it
// when the SpanTreeView stops rendering it.
func (fe *frontendPretty) syncSpinnerTree(st *SpanTreeView) {
	row := fe.rows.BySpan[st.spanID]
	running := row != nil && row.Span.IsRunningOrEffectsRunning()
	if running && st.spinner == nil {
		st.spinner = &SpinnerView{
			rave: fe.spinner,
			now:  time.Now(),
		}
	} else if !running && st.spinner != nil {
		st.spinner = nil
	}
}

func (fe *frontendPretty) SetClient(client *dagger.Client) {
	fe.tui.Dispatch(func() {
		fe.dag = client
	})
}

func NewPretty(w io.Writer) Frontend {
	return NewWithDB(w, dagui.NewDB())
}

func NewReporter(w io.Writer) Frontend {
	fe := NewWithDB(w, dagui.NewDB())
	fe.reportOnly = true
	return fe
}

func NewWithDB(w io.Writer, db *dagui.DB) *frontendPretty {
	profile := ColorProfile()
	return &frontendPretty{
		db:        db,
		logs:      newPrettyLogs(profile, db),
		autoFocus: true,

		// set empty initial row state to avoid nil checks
		rowsView: &dagui.RowsView{},
		rows:     &dagui.Rows{BySpan: map[dagui.SpanID]*dagui.TraceRow{}},

		// initial TUI state
		window:     windowSize{Width: -1, Height: -1}, // be clear that it's not set
		spinner:    NewRave(),
		profile:    profile,
		browserBuf:    new(strings.Builder),
		notifications: make(map[string]*NotificationBubble),
		overlayHandles: make(map[string]*tuist.OverlayHandle),
		writer:     w,
		shownErrs:  map[dagui.SpanID]bool{},
	}
}

func (fe *frontendPretty) SetSidebarContent(section SidebarSection) {
	fe.tui.Dispatch(func() {
		title := section.Title

		if bubble, ok := fe.notifications[title]; ok {
			// Update existing bubble
			bubble.section = section
			bubble.Update()
		} else {
			// Create new bubble
			bubble := newNotificationBubble(fe, section)
			fe.notifications[title] = bubble

			// Track order: untitled goes first, titled appends
			if title == "" {
				fe.notificationOrder = append([]string{""}, fe.notificationOrder...)
			} else {
				fe.notificationOrder = append(fe.notificationOrder, title)
			}

			// Show as overlay, anchored to bottom-right, content-relative
			fe.overlayHandles[title] = fe.tui.ShowOverlay(bubble, &tuist.OverlayOptions{
				Width:           tuist.SizeAbs(notificationWidth(fe.window.Width)),
				Anchor:          tuist.AnchorBottomRight,
				ContentRelative: true,
				Margin:          tuist.OverlayMargin{Bottom: fe.notificationStackOffset(title), Right: 1},
			})
		}

		// Recompute all overlay positions (stacking)
		fe.repositionNotifications()
		fe.Compo.Update()
	})
}

// notificationStackOffset computes the vertical offset for a notification
// bubble so they stack upward from the bottom-right.
func (fe *frontendPretty) notificationStackOffset(targetTitle string) int {
	offset := 0
	for _, title := range fe.notificationOrder {
		if title == targetTitle {
			return offset
		}
		bubble := fe.notifications[title]
		if bubble != nil {
			content := bubble.section.Body(notificationWidth(fe.window.Width) - 4)
			if content != "" {
				contentLines := strings.Split(strings.TrimRight(content, "\n"), "\n")
				offset += len(contentLines) + 3 // content + top border + bottom border + gap
			}
		}
	}
	return offset
}

// repositionNotifications updates overlay positions for all notification
// bubbles so they stack correctly.
func (fe *frontendPretty) repositionNotifications() {
	w := notificationWidth(fe.window.Width)
	for _, title := range fe.notificationOrder {
		handle, ok := fe.overlayHandles[title]
		if !ok {
			continue
		}
		handle.SetOptions(&tuist.OverlayOptions{
			Width:           tuist.SizeAbs(w),
			Anchor:          tuist.AnchorBottomRight,
			ContentRelative: true,
			Margin:          tuist.OverlayMargin{Bottom: fe.notificationStackOffset(title), Right: 1},
		})
	}
}

var sidebarBG lipgloss.TerminalColor

func init() {
	// delegate notification background to editline background
	focusedStyle, _ := editline.DefaultStyles()
	editlineStyle := focusedStyle.Editor.CursorLine
	sidebarBG = editlineStyle.GetBackground()
}

func (fe *frontendPretty) Shell(ctx context.Context, handler ShellHandler) {
	fe.tui.Dispatch(func() {
		fe.startShell(ctx, handler)
		fe.Compo.Update()
	})
	<-ctx.Done()
}

func (fe *frontendPretty) startShell(ctx context.Context, handler ShellHandler) {
	fe.shell = handler
	fe.shellCtx = ctx
	fe.promptFg = termenv.ANSIGreen

	fe.initEditline()

	// restore history
	fe.editline.MaxHistorySize = 1000
	if history, err := history.LoadHistory(historyFile); err == nil {
		fe.editline.SetHistory(history)
	}
	fe.editline.HistoryEncoder = handler

	// wire up auto completion
	fe.editline.AutoComplete = handler.AutoComplete

	// if input ends with a pipe, then it's not complete
	fe.editline.CheckInputComplete = handler.IsComplete

	// put the bowtie on
	fe.updatePrompt()
	fe.execTeaCmd(fe.editline.Focus())
}

func (fe *frontendPretty) SetCloudURL(ctx context.Context, url string, msg string, logged bool) {
	if fe.OpenWeb {
		if err := browser.OpenURL(url); err != nil {
			slog.Warn("failed to open URL", "url", url, "err", err)
		}
	}
	fe.tui.Dispatch(func() {
		fe.cloudURL = url
		if msg != "" {
			slog.Warn(msg)
		}

		if cmdContext, ok := FromCmdContext(ctx); ok && cmdContext.printTraceLink {
			if logged {
				fe.msgPreFinalRender.WriteString(traceMessage(fe.profile, url, msg))
			} else if !skipLoggedOutTraceMsg() {
				fe.msgPreFinalRender.WriteString(fmt.Sprintf(loggedOutTraceMsg, url))
			}
		}
		fe.Compo.Update()
	})
}

func traceMessage(profile termenv.Profile, url string, msg string) string {
	buffer := &bytes.Buffer{}
	out := NewOutput(buffer, termenv.WithProfile(profile))

	fmt.Fprint(buffer, out.String("Full trace at ").Bold().String())
	fmt.Fprint(buffer, url)
	if msg != "" {
		fmt.Fprintf(buffer, " (%s)", msg)
	}

	return buffer.String()
}

// Run starts the TUI, calls the run function, stops the TUI, and finally
// prints the primary output to the appropriate stdout/stderr streams.
func (fe *frontendPretty) Run(ctx context.Context, opts dagui.FrontendOpts, run func(context.Context) (cleanups.CleanupF, error)) error {
	if opts.TooFastThreshold == 0 {
		opts.TooFastThreshold = 100 * time.Millisecond
	}
	if opts.GCThreshold == 0 {
		opts.GCThreshold = 1 * time.Second
	}
	fe.FrontendOpts = opts

	if fe.reportOnly {
		cleanup, err := run(ctx)
		if cleanup != nil {
			err = errors.Join(err, cleanup())
		}
		fe.err = err
	} else {
		// run the function wrapped in the TUI
		fe.err = fe.runWithTUI(ctx, run)
	}

	if fe.editline != nil && fe.shell != nil {
		if err := os.MkdirAll(filepath.Dir(historyFile), 0755); err != nil {
			slog.Error("failed to create history directory", "err", err)
		}
		if err := history.SaveHistory(fe.editline.GetHistory(), historyFile); err != nil {
			slog.Error("failed to save history", "err", err)
		}
	}

	// print the final output display to stderr
	if renderErr := fe.FinalRender(os.Stderr); renderErr != nil {
		return renderErr
	}

	fe.db.WriteDot(opts.DotOutputFilePath, opts.DotFocusField, opts.DotShowInternal)

	// return original err
	return normalizeFrontendExit(fe.err, fe.db)
}

func (fe *frontendPretty) HandlePrompt(ctx context.Context, title, prompt string, dest any) error {
	switch x := dest.(type) {
	case *bool:
		return fe.handlePromptBool(ctx, title, prompt, x)
	case *string:
		return fe.handlePromptString(ctx, title, prompt, x)
	default:
		return fmt.Errorf("unsupported prompt destination type: %T", dest)
	}
}

func (fe *frontendPretty) HandleForm(ctx context.Context, form *huh.Form) error {
	done := make(chan struct{}, 1)

	fe.tui.Dispatch(func() {
		fe.handlePromptForm(form, func(f *huh.Form) {
			close(done)
		})
		fe.Compo.Update()
	})

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

func (fe *frontendPretty) handlePromptForm(form *huh.Form, result func(*huh.Form)) {
	form.SubmitCmd = func() tea.Msg {
		result(form)
		return promptDone{}
	}
	form.CancelCmd = func() tea.Msg {
		result(form)
		return promptDone{}
	}
	fe.form = form.WithTheme(huh.ThemeBase16()).WithShowHelp(false)
	fe.execTeaCmd(fe.form.Init())
}

func (fe *frontendPretty) Opts() *dagui.FrontendOpts {
	return &fe.FrontendOpts
}

func (fe *frontendPretty) SetVerbosity(n int) {
	fe.tui.Dispatch(func() {
		fe.Opts().Verbosity = n
		fe.Compo.Update()
	})
}

func (fe *frontendPretty) SetPrimary(spanID dagui.SpanID) {
	fe.tui.Dispatch(func() {
		fe.db.SetPrimarySpan(spanID)
		fe.ZoomedSpan = spanID
		fe.FocusedSpan = spanID
		fe.recalculateViewLocked()
		fe.Compo.Update()
	})
}

func (fe *frontendPretty) RevealAllSpans() {
	fe.tui.Dispatch(func() {
		fe.ZoomedSpan = dagui.SpanID{}
		fe.Compo.Update()
	})
}

func (fe *frontendPretty) runWithTUI(ctx context.Context, run func(context.Context) (cleanups.CleanupF, error)) (rerr error) {
	// wire up the run so we can call it asynchronously with the TUI running
	fe.run = run
	// set up ctx cancellation so the TUI can interrupt via keypresses
	fe.runCtx, fe.interrupt = context.WithCancelCause(ctx)

	fe.quit = make(chan struct{})
	fe.backgroundReq = make(chan backgroundRequest)

	in, _ := findTTYs()
	if in == nil {
		tty, err := openInputTTY()
		if err != nil {
			return err
		}
		if tty != nil {
			in = tty
			defer tty.Close()
		}
	}
	// store in fe to use in Background processing
	fe.stdin = in

	// prevent browser.OpenURL from breaking the TUI if it fails
	browser.Stdout = fe.browserBuf
	browser.Stderr = fe.browserBuf

	// Create and start the TUI
	fe.startTUI()

	// Main loop: wait for quit or background requests
	for {
		select {
		case <-fe.quit:
			fe.tui.Stop()

			// if the ctx was canceled, we don't need to return whatever random garbage
			// error string we got back; just return the ctx err.
			if fe.runCtx.Err() != nil {
				return context.Cause(fe.runCtx)
			}
			return fe.err

		case req := <-fe.backgroundReq:
			// Suspend TUI for background command
			fe.tui.Stop()

			req.cmd.SetStdin(os.Stdin)
			req.cmd.SetStdout(os.Stdout)
			req.cmd.SetStderr(os.Stderr)

			var err error
			if req.raw {
				if stdin, ok := fe.stdin.(*os.File); ok {
					oldState, rawErr := term.MakeRaw(int(stdin.Fd()))
					if rawErr != nil {
						err = rawErr
					} else {
						err = req.cmd.Run()
						if oldState != nil {
							term.Restore(int(stdin.Fd()), oldState)
						}
					}
				} else {
					err = req.cmd.Run()
				}
			} else {
				err = req.cmd.Run()
			}

			req.done <- err

			// Restart TUI
			fe.startTUI()
		}
	}
}

func (fe *frontendPretty) startTUI() {
	terminal := tuist.NewProcessTerminal()
	fe.tui = tuist.New(terminal)
	if p := os.Getenv("TUIST_LOG"); p != "" {
		if f, err := os.Create(p); err == nil {
			fe.tui.SetDebugWriter(f)
		}
	}
	fe.tui.AddChild(fe)
	fe.tui.SetFocus(fe)
	fe.tui.Start()
}

// OnMount is called by tuist when the component is mounted into the TUI tree.
// It starts the frame ticker and, on the first mount, spawns the run function.
func (fe *frontendPretty) OnMount(ctx tuist.EventContext) {
	if !fe.spawned {
		fe.spawned = true
		// Spawn the run function
		go fe.spawnRun()
	}
}

// scheduleKeypressClear starts a one-shot timer that re-renders the keymap
// after the keypress highlight fades. Replaces the old polling frameLoop.
func (fe *frontendPretty) scheduleKeypressClear() {
	go func() {
		time.Sleep(keypressDuration + 50*time.Millisecond)
		fe.tui.Dispatch(func() {
			fe.Compo.Update()
		})
	}()
}

func (fe *frontendPretty) spawnRun() {
	cleanup, err := fe.run(fe.runCtx)
	fe.tui.Dispatch(func() {
		if !fe.NoExit || fe.interrupted {
			if cleanup != nil {
				go func() {
					if cleanErr := cleanup(); cleanErr != nil {
						slog.Error("cleanup failed", "err", cleanErr)
					}
					fe.tui.Dispatch(func() {
						fe.handleDone(err)
					})
				}()
			} else {
				fe.handleDone(err)
			}
		} else {
			fe.cleanup = func() {
				if cleanup != nil {
					if cleanErr := cleanup(); cleanErr != nil {
						slog.Error("cleanup failed", "err", cleanErr)
					}
				}
			}
			fe.handleDone(err)
		}
	})
}

func (fe *frontendPretty) handleDone(err error) {
	slog.Debug("run finished", "err", err)
	fe.done = true
	fe.err = err
	if fe.eof && (!fe.NoExit || fe.interrupted) {
		fe.quitting = true
		fe.doQuit()
	}
	fe.Compo.Update()
}

func (fe *frontendPretty) handleEOF() {
	slog.Debug("got EOF")
	fe.eof = true
	if fe.done && (!fe.NoExit || fe.interrupted) {
		fe.quitting = true
		fe.doQuit()
	}
	fe.Compo.Update()
}

func (fe *frontendPretty) doQuit() {
	select {
	case <-fe.quit:
		// already closed
	default:
		close(fe.quit)
	}
}

// FinalRender is called after the program has finished running and prints the
// final output after the TUI has exited.
func (fe *frontendPretty) FinalRender(w io.Writer) error {
	// Hint for future rendering that this is the final, non-interactive render
	// (so don't show key hints etc.)
	fe.finalRender = true

	// Render the full trace.
	fe.ZoomedSpan = fe.db.PrimarySpan
	fe.recalculateViewLocked()

	// Unfocus for the final render.
	fe.FocusedSpan = dagui.SpanID{}

	r := newRenderer(fe.db, fe.contentWidth/2, fe.FrontendOpts, true)

	out := NewOutput(w, termenv.WithProfile(fe.profile))

	if fe.Debug || fe.Verbosity >= dagui.ShowCompletedVerbosity || fe.err != nil {
		fe.renderProgressFinal(out, r)

		if fe.msgPreFinalRender.Len() > 0 {
			defer func() {
				fmt.Fprintln(w)
				handleTelemetryErrorOutput(w, out, fe.TelemetryError)
				fmt.Fprintln(os.Stderr, fe.msgPreFinalRender.String())
			}()
		}
	}

	if fe.err != nil && fe.shell == nil {
		if fe.hasShownRootError() {
			// If we've already shown the root cause error for the command, we can
			// skip displaying the primary output and error, since it's just a poorer
			// representation of the same error (`Error: input: ...`)
			var exitErr ExitError
			if errors.As(fe.err, &exitErr) {
				return exitErr
			}
			return ExitError{OriginalCode: 1, Original: fe.err}
		}
	}

	// Replay the primary output log to stdout/stderr.
	return renderPrimaryOutput(w, fe.db)
}

func (fe *frontendPretty) SpanExporter() sdktrace.SpanExporter {
	return prettySpanExporter{fe}
}

type prettySpanExporter struct {
	*frontendPretty
}

func (fe prettySpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	// Copy the slice — the OTel SDK reuses it after ExportSpans returns,
	// and Dispatch runs asynchronously on the UI goroutine.
	spansCopy := make([]sdktrace.ReadOnlySpan, len(spans))
	copy(spansCopy, spans)
	spanIDs := make([]dagui.SpanID, len(spans))
	for i, s := range spans {
		spanIDs[i] = dagui.SpanID{SpanID: s.SpanContext().SpanID()}
	}
	fe.tui.Dispatch(func() {
		fe.db.ExportSpans(context.Background(), spansCopy)
		for _, id := range spanIDs {
			if sr, ok := fe.spanTrees[id]; ok {
				sr.Update()
			}
		}
		fe.recalculateViewLocked()
		fe.Compo.Update()
	})
	return nil
}

func (fe *frontendPretty) Shutdown(ctx context.Context) error {
	if err := fe.db.Shutdown(ctx); err != nil {
		return err
	}
	return fe.Close()
}

func (fe *frontendPretty) LogExporter() sdklog.Exporter {
	return prettyLogExporter{fe}
}

type prettyLogExporter struct {
	*frontendPretty
}

func (fe prettyLogExporter) Export(ctx context.Context, logs []sdklog.Record) error {
	// Copy the slice — the OTel SDK reuses it after Export returns.
	logsCopy := make([]sdklog.Record, len(logs))
	copy(logsCopy, logs)
	fe.tui.Dispatch(func() {
		for _, log := range logsCopy {
			if log.SpanID().IsValid() {
				spanID := dagui.SpanID{SpanID: log.SpanID()}
				if sr, ok := fe.spanTrees[spanID]; ok {
					sr.Update()
				}
				// Also mark roll-up parent spans dirty
				if _, rolledUp := fe.logs.findRollUpSpan(spanID); rolledUp {
					for id := spanID; ; {
						span := fe.db.Spans.Map[id]
						if span == nil || span.Boundary || span.Encapsulate || span.Internal {
							break
						}
						if span.RollUpLogs {
							if sr, ok := fe.spanTrees[id]; ok {
								sr.Update()
							}
							break
						}
						if !span.ParentID.IsValid() {
							break
						}
						id = span.ParentID
					}
				}
			}
		}
		fe.db.LogExporter().Export(context.Background(), logsCopy)
		fe.logs.Export(context.Background(), logsCopy)
		fe.Compo.Update()
	})
	return nil
}

func (fe *frontendPretty) ForceFlush(context.Context) error {
	return nil
}

func (fe *frontendPretty) Close() error {
	if fe.tui != nil {
		fe.tui.Dispatch(func() {
			fe.handleEOF()
		})
	}
	return nil
}

func (fe *frontendPretty) MetricExporter() sdkmetric.Exporter {
	return FrontendMetricExporter{fe}
}

type FrontendMetricExporter struct {
	*frontendPretty
}

func (fe FrontendMetricExporter) Export(ctx context.Context, resourceMetrics *metricdata.ResourceMetrics) error {
	// Synchronous dispatch — the metrics SDK reuses the ResourceMetrics struct,
	// and deep-copying it is impractical. Wait for the UI goroutine to consume it.
	done := make(chan struct{})
	fe.tui.Dispatch(func() {
		fe.db.MetricExporter().Export(ctx, resourceMetrics)
		fe.Compo.Update()
		close(done)
	})
	<-done
	return nil
}

func (fe FrontendMetricExporter) Temporality(ik sdkmetric.InstrumentKind) metricdata.Temporality {
	return fe.db.Temporality(ik)
}

func (fe FrontendMetricExporter) Aggregation(ik sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return fe.db.Aggregation(ik)
}

func (fe FrontendMetricExporter) ForceFlush(context.Context) error {
	return nil
}

func (fe *frontendPretty) Background(cmd tea.ExecCommand, raw bool) error {
	errs := make(chan error, 1)
	fe.backgroundReq <- backgroundRequest{
		cmd:  cmd,
		raw:  raw,
		done: errs,
	}
	return <-errs
}

var KeymapStyle = lipgloss.NewStyle().
	Foreground(lipgloss.ANSIColor(termenv.ANSIBrightBlack))

const keypressDuration = 500 * time.Millisecond

func (fe *frontendPretty) renderKeymap(out io.Writer, style lipgloss.Style, keys []key.Binding) int {
	w := new(strings.Builder)
	var showedKey bool
	for _, key := range keys {
		mainKey := key.Keys()[0]
		var pressed bool
		if time.Since(fe.pressedKeyAt) < keypressDuration {
			pressed = slices.Contains(key.Keys(), fe.pressedKey)
		}
		if !key.Enabled() && !pressed {
			continue
		}
		keyStyle := style
		if pressed {
			keyStyle = keyStyle.Foreground(nil)
		}
		if showedKey {
			fmt.Fprint(w, style.Render(" "+DotTiny+" "))
		}
		fmt.Fprint(w, keyStyle.Bold(true).Render(mainKey))
		fmt.Fprint(w, keyStyle.Render(" "+key.Help().Desc))
		showedKey = true
	}
	res := w.String()
	fmt.Fprint(out, res)
	return lipgloss.Width(res)
}

func (fe *frontendPretty) keys(out *termenv.Output) []key.Binding {
	if fe.form != nil {
		return fe.form.KeyBinds()
	}

	if fe.editlineFocused {
		bnds := []key.Binding{
			key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "nav mode")),
		}
		if fe.shell != nil {
			bnds = append(bnds, fe.shell.KeyBindings(out)...)
		}
		return bnds
	}

	var quitMsg string
	if fe.interrupted {
		quitMsg = "quit!"
	} else if fe.shell != nil {
		quitMsg = "interrupt"
	} else {
		quitMsg = "quit"
	}

	noExitHelp := "no exit"
	if fe.NoExit {
		color := termenv.ANSIYellow
		if fe.done || fe.interrupted {
			color = termenv.ANSIRed
		}
		noExitHelp = out.String(noExitHelp).Foreground(color).String()
	}
	var focused *dagui.Span
	if fe.FocusedSpan.IsValid() {
		focused = fe.db.Spans.Map[fe.FocusedSpan]
	}
	return []key.Binding{
		key.NewBinding(key.WithKeys("i", "tab"),
			key.WithHelp("i", "input mode"),
			KeyEnabled(fe.shell != nil)),
		key.NewBinding(key.WithKeys("w"),
			key.WithHelp("w", out.Hyperlink(fe.cloudURL, "web")),
			KeyEnabled(fe.cloudURL != "")),
		key.NewBinding(key.WithKeys("←↑↓→", "up", "down", "left", "right", "h", "j", "k", "l"),
			key.WithHelp("←↑↓→", "move")),
		key.NewBinding(key.WithKeys("home"),
			key.WithHelp("home", "first")),
		key.NewBinding(key.WithKeys("end", " "),
			key.WithHelp("end", "last")),
		key.NewBinding(key.WithKeys("+/-", "+", "-"),
			key.WithHelp("+/-", fmt.Sprintf("verbosity=%d", fe.Verbosity))),
		key.NewBinding(key.WithKeys("E"),
			key.WithHelp("E", noExitHelp)),
		key.NewBinding(key.WithKeys("q", "ctrl+c"),
			key.WithHelp("q", quitMsg)),
		key.NewBinding(key.WithKeys("esc"),
			key.WithHelp("esc", "unzoom"),
			KeyEnabled(fe.ZoomedSpan.IsValid() && fe.ZoomedSpan != fe.db.PrimarySpan)),
		key.NewBinding(key.WithKeys("r"),
			key.WithHelp("r", "go to error"),
			KeyEnabled(focused != nil && len(focused.ErrorOrigins.Order) > 0)),
		key.NewBinding(key.WithKeys("t"),
			key.WithHelp("t", "start terminal"),
			KeyEnabled(focused != nil && fe.terminalCallback(focused) != nil),
		),
	}
}

func KeyEnabled(enabled bool) key.BindingOpt {
	return func(b *key.Binding) {
		b.SetEnabled(enabled)
	}
}

// ---------- tuist.Component -------------------------------------------------

// Render implements tuist.Component. It produces the full TUI output as lines.
func (fe *frontendPretty) Render(ctx tuist.RenderContext) tuist.RenderResult {
	if fe.backgrounded || fe.quitting {
		return tuist.RenderResult{}
	}

	// Update window dimensions from tuist
	fe.window = windowSize{Width: ctx.Width, Height: ctx.ScreenHeight}
	fe.setWindowSizeLocked(fe.window)

	r := newRenderer(fe.db, fe.contentWidth/2, fe.FrontendOpts, false)

	lines := ctx.Recycle

	// Zoom header
	var progPrefix string
	if fe.rowsView != nil && fe.rowsView.Zoomed != nil && fe.rowsView.Zoomed.ID != fe.db.PrimarySpan {
		zoomBuf := new(strings.Builder)
		zoomOut := NewOutput(zoomBuf, termenv.WithProfile(fe.profile))
		fe.renderStep(zoomOut, r, &dagui.TraceRow{
			Span:     fe.rowsView.Zoomed,
			Expanded: true,
		}, "")
		lines = appendView(lines, zoomBuf.String())
		progPrefix = "  "
	}

	// Pre-render chrome below progress to measure its height for truncation.
	logsLines := fe.renderLogsLines(progPrefix)
	editlineLines := fe.renderEditlineLines()
	formLines := fe.renderFormLines()
	keymapLines := fe.renderKeymapLines()

	chromeHeight := len(keymapLines) + 1 // +1 for gap line after progress
	if len(logsLines) > 0 {
		chromeHeight += len(logsLines) + 1
	}
	chromeHeight += len(editlineLines)
	chromeHeight += len(formLines)

	// Render progress rows via tree-based components
	progressLines := fe.renderProgressLines(r, ctx, chromeHeight, progPrefix)
	if len(progressLines) > 0 {
		lines = append(lines, progressLines...)
		lines = append(lines, "") // gap line after progress
	}

	// Append chrome
	if len(logsLines) > 0 {
		lines = append(lines, logsLines...)
		lines = append(lines, "") // trailing gap
	}
	lines = append(lines, editlineLines...)
	lines = append(lines, formLines...)
	// Ensure there's a blank line before keymap if needed
	if len(lines) > 0 && lines[len(lines)-1] != "" && len(keymapLines) > 0 {
		lines = append(lines, "")
	}
	lines = append(lines, keymapLines...)

	// Truncate each line to terminal width so no line wraps to multiple
	// physical rows. Without this, tuist's diff renderer miscounts
	// cursor positions.
	if fe.window.Width > 0 {
		w := uint(fe.window.Width)
		for i, line := range lines {
			lines[i] = truncate.String(line, w)
		}
	}
	return tuist.RenderResult{Lines: lines}
}

// appendView splits a string view into lines and appends them.
func appendView(lines []string, view string) []string {
	if view == "" {
		return lines
	}
	return append(lines, strings.Split(strings.TrimSuffix(view, "\n"), "\n")...)
}

// renderLogsLines returns the zoomed span's log output as lines.
func (fe *frontendPretty) renderLogsLines(prefix string) []string {
	logs := fe.logs.Logs[fe.ZoomedSpan]
	if logs == nil || logs.UsedHeight() == 0 || fe.hasShownRootError() {
		return nil
	}
	logs.SetHeight(fe.window.Height / 3)
	logs.SetPrefix(prefix)
	view := logs.View()
	if view == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(view, "\n"), "\n")
}

// renderEditlineLines returns the editline view as lines.
func (fe *frontendPretty) renderEditlineLines() []string {
	if fe.editline == nil {
		return nil
	}
	return appendView(nil, fe.editlineView())
}

// renderFormLines returns the form view as lines.
func (fe *frontendPretty) renderFormLines() []string {
	if fe.form == nil {
		return nil
	}
	return appendView(nil, fe.formView())
}

// renderKeymapLines returns the keymap view as lines.
func (fe *frontendPretty) renderKeymapLines() []string {
	return appendView(nil, fe.keymapView())
}

func (fe *frontendPretty) keymapView() string {
	outBuf := new(strings.Builder)
	out := NewOutput(outBuf, termenv.WithProfile(fe.profile))
	if fe.UsingCloudEngine {
		fmt.Fprint(out, lipgloss.NewStyle().
			Foreground(lipgloss.ANSIColor(termenv.ANSIBrightMagenta)).
			Render(CloudIcon+" cloud"))
		fmt.Fprint(out, KeymapStyle.Render(" "+VertBoldDash3+" "))
	}
	fe.renderKeymap(out, KeymapStyle, fe.keys(out))
	return outBuf.String()
}

func (fe *frontendPretty) recalculateViewLocked() {
	fe.rowsView = fe.db.RowsView(fe.FrontendOpts)
	fe.rows = fe.rowsView.Rows(fe.FrontendOpts)

	if len(fe.rows.Order) == 0 {
		fe.focusedIdx = -1
		fe.FocusedSpan = dagui.SpanID{}
		return
	}
	if len(fe.rows.Order) < fe.focusedIdx {
		// durability: everything disappeared?
		fe.autoFocus = true
	}
	if fe.autoFocus {
		fe.focusedIdx = len(fe.rows.Order) - 1
		fe.FocusedSpan = fe.rows.Order[fe.focusedIdx].Span.ID
	} else if row := fe.rows.BySpan[fe.FocusedSpan]; row != nil {
		fe.focusedIdx = row.Index
	} else {
		// lost focus somehow
		fe.autoFocus = true
		fe.recalculateViewLocked()
		return
	}

	// Sync the SpanTreeView component tree with the current rowsView.
	// This is where ALL component state mutations happen — prefix,
	// children, focus, spinners. Render() is then a pure read.
	fe.syncSpanTreeState()
}

// syncSpanTreeState synchronizes the SpanTreeView component tree with
// the current rowsView and rows. Called from recalculateViewLocked()
// (i.e., from event handlers and Dispatch callbacks, never from Render).
//
// It walks the TraceTree top-down, creating/reusing SpanTreeViews,
// computing prefixes, and calling Update() on components whose
// visible state changed.
func (fe *frontendPretty) syncSpanTreeState() {
	if fe.spanTrees == nil {
		fe.spanTrees = make(map[dagui.SpanID]*SpanTreeView)
	}

	// When global config changes (verbosity, zoom, etc.), mark all
	// existing trees dirty so they re-render with the new settings.
	if fe.renderVersion != fe.lastRenderedVersion {
		fe.lastRenderedVersion = fe.renderVersion
		for _, st := range fe.spanTrees {
			st.Update()
		}
	}

	// Determine the zoom prefix for top-level trees.
	var zoomPrefix string
	if fe.rowsView.Zoomed != nil && fe.rowsView.Zoomed.ID != fe.db.PrimarySpan {
		zoomPrefix = "  "
	}

	body := fe.rowsView.Body
	newTops := make([]*SpanTreeView, 0, len(body))
	for _, tree := range body {
		st := fe.getOrCreateSpanTree(tree.Span.ID)

		// Top-level prefix (zoom indentation if applicable)
		var newPrefix treePrefix
		if zoomPrefix != "" {
			newPrefix = treePrefix{
				step:        zoomPrefix,
				cont:        zoomPrefix,
				forChildren: zoomPrefix,
				contWidth:   lipgloss.Width(zoomPrefix),
			}
		}
		fe.syncTreeNode(st, newPrefix)
		newTops = append(newTops, st)
	}
	fe.topTrees = newTops
}

// syncTreeNode recursively syncs a SpanTreeView and its children with
// the current trace data. Updates prefix, focus, spinner, and children.
// Calls Update() on any SpanTreeView whose visible state changed.
func (fe *frontendPretty) syncTreeNode(st *SpanTreeView, newPrefix treePrefix) {
	changed := false

	// Sync prefix
	if st.prefix != newPrefix {
		st.prefix = newPrefix
		changed = true
	}

	// Sync focus
	isFocused := st.spanID == fe.FocusedSpan && !fe.editlineFocused && fe.form == nil
	if st.focused != isFocused {
		st.focused = isFocused
		changed = true
	}

	// Sync spinner
	fe.syncSpinnerTree(st)

	if changed {
		st.Update()
	}

	// Sync children for expanded nodes
	row := fe.rows.BySpan[st.spanID]
	tree := fe.rowsView.BySpan[st.spanID]
	if row == nil || tree == nil || !row.Expanded {
		// Collapsed: clear children so they get dismounted on next render
		st.children = nil
		return
	}

	// Determine visible children
	var childTrees []*dagui.TraceTree
	if tree.ShouldShowRevealedSpans(fe.FrontendOpts) {
		for _, revealedSpan := range tree.Span.RevealedSpans.Order {
			if revealedTree, ok := fe.rowsView.BySpan[revealedSpan.ID]; ok {
				childTrees = append(childTrees, revealedTree)
			}
		}
	} else {
		childTrees = tree.Children
	}

	// Compute the gap prefix for lines between this node's children.
	// This is the ancestor bars + this node's own bar column (always
	// shown, since we're between children that both exist).
	out := NewOutput(io.Discard, termenv.WithProfile(fe.profile))
	span := row.Span
	color := restrainedStatusColor(span)
	if !span.Reveal && len(span.RevealedSpans.Order) == 0 {
		st.childrenGapPrefix = st.prefix.forChildren + out.String(VertBar+" ").Foreground(color).Faint().String()
	} else {
		st.childrenGapPrefix = st.prefix.forChildren + "  "
	}

	// Reconcile child SpanTreeViews
	if st.childMap == nil {
		st.childMap = make(map[dagui.SpanID]*SpanTreeView)
	}
	newChildren := make([]*SpanTreeView, 0, len(childTrees))
	seen := make(map[dagui.SpanID]bool, len(childTrees))
	for i, childTree := range childTrees {
		id := childTree.Span.ID
		seen[id] = true
		child, ok := st.childMap[id]
		if !ok {
			child = &SpanTreeView{
				fe:     fe,
				spanID: id,
			}
			st.childMap[id] = child
			fe.spanTrees[id] = child
		}

		// Compute child prefix
		hasNext := i < len(childTrees)-1
		childPrefix := st.computeChildPrefix(out, hasNext)

		// Recurse
		fe.syncTreeNode(child, childPrefix)
		newChildren = append(newChildren, child)
	}
	for id := range st.childMap {
		if !seen[id] {
			delete(st.childMap, id)
		}
	}
	st.children = newChildren
}

// renderProgressLines renders progress using the tree-based SpanTreeView
// components and returns the output as lines. Truncates below the focused
// item so it stays onscreen.
func (fe *frontendPretty) renderProgressLines(r *renderer, ctx tuist.RenderContext, chromeHeight int, prefix string) []string {
	if fe.rowsView == nil {
		return nil
	}

	// topTrees was synced by syncSpanTreeState() in recalculateViewLocked().

	// Render all top-level trees via RenderChild, assembling into allLines.
	// We render everything (for caching), then truncate below the focused
	// item so it stays onscreen. Content above scrolls into scrollback.
	var allLines []string
	topGapCounts := make([]int, len(fe.topTrees))
	for i, treeView := range fe.topTrees {
		childCtx := tuist.RenderContext{
			Width:        fe.contentWidth,
			ScreenHeight: ctx.ScreenHeight,
		}
		result := fe.RenderChild(treeView, childCtx)

		// Gap between top-level trees
		if i > 0 && len(result.Lines) > 0 {
			row := fe.rows.BySpan[treeView.spanID]
			if row != nil {
				gaps := fe.renderTreeGap(r, row, treeView.prefix.cont)
				topGapCounts[i] = len(gaps)
				allLines = append(allLines, gaps...)
			}
		}

		allLines = append(allLines, result.Lines...)
	}

	if len(allLines) == 0 {
		return nil
	}

	// Find the focused line by walking the tree structure.
	focusLine := -1
	if fe.FocusedSpan.IsValid() {
		offset := 0
		for i, tree := range fe.topTrees {
			offset += topGapCounts[i]
			if line := fe.findFocusInSubtree(tree, offset); line >= 0 {
				focusLine = line
				break
			}
			offset += tree.totalLineCount()
		}
	}

	// Truncate content below focus so the focused item stays onscreen.
	// Everything above renders into terminal scrollback naturally.
	viewportHeight := ctx.ScreenHeight - chromeHeight
	if viewportHeight < 1 {
		viewportHeight = 1
	}
	end := len(allLines)
	if focusLine >= 0 {
		// Allow some context below focus (half the viewport), but cap
		// the total so focus doesn't get pushed above the viewport.
		afterBudget := viewportHeight / 2
		if focusLine+afterBudget < end {
			end = focusLine + afterBudget
		}
	}

	return allLines[:end]
}




// totalLineCount returns the total number of rendered lines for a SpanTreeView,
// including self content, gap lines, and all children.
func (st *SpanTreeView) totalLineCount() int {
	n := st.selfLineCount
	for i := range st.children {
		n += st.childGapCounts[i] + st.childLineCounts[i]
	}
	return n
}

// findFocusInSubtree recursively searches for the focused span in the tree,
// returning its line offset relative to the given base offset, or -1 if not found.
func (fe *frontendPretty) findFocusInSubtree(st *SpanTreeView, offset int) int {
	if st.spanID == fe.FocusedSpan {
		return offset
	}
	offset += st.selfLineCount
	for i, child := range st.children {
		offset += st.childGapCounts[i]
		if line := fe.findFocusInSubtree(child, offset); line >= 0 {
			return line
		}
		offset += st.childLineCounts[i]
	}
	return -1
}

// renderTreeGap renders the gap line(s) that precede a row in tree rendering.
// This replaces renderRowGap for tree-based rendering, using the tree prefix
// instead of calling fancyIndent.
func (fe *frontendPretty) renderTreeGap(r *renderer, row *dagui.TraceRow, gapPrefix string) []string {
	if fe.shell != nil {
		if row.Depth == 0 && row.Previous != nil {
			return []string{""}
		}
		return nil
	}
	if row.PreviousVisual != nil &&
		row.PreviousVisual.Depth >= row.Depth &&
		!row.Chained &&
		(row.PreviousVisual.Depth > row.Depth ||
			row.Span.Call() != nil ||
			row.Span.CheckName != "" ||
			row.Span.GeneratorName != "" ||
			(row.PreviousVisual.Span.Call() != nil && row.Span.Call() == nil) ||
			(row.PreviousVisual.Span.Message != "" && row.Span.Message != "") ||
			(row.PreviousVisual.Span.Message == "" && row.Span.Message != "")) {
		return []string{gapPrefix}
	}
	return nil
}

func (fe *frontendPretty) focus(row *dagui.TraceRow) {
	if row == nil {
		return
	}
	fe.FocusedSpan = row.Span.ID
	fe.focusedIdx = row.Index
	// Set tuist-level focus for keyboard event bubbling
	if sr, ok := fe.spanTrees[row.Span.ID]; ok && fe.tui != nil {
		fe.tui.SetFocus(sr)
	}
	// syncSpanTreeState (called by recalculate) will sync .focused on
	// all nodes and call Update() on the ones that changed.
	fe.recalculateViewLocked()
}

// ---------- tuist.Interactive -----------------------------------------------

// HandleKeyPress implements tuist.Interactive. It dispatches key events to the
// appropriate handler based on the current mode (form, editline, or nav).
func (fe *frontendPretty) HandleKeyPress(_ tuist.EventContext, ev uv.KeyPressEvent) bool {
	switch {
	case fe.form != nil:
		fe.handleFormKey(ev)
	case fe.editlineFocused:
		fe.handleEditlineKeyUV(ev)
	default:
		fe.handleNavKeyUV(ev)
	}

	// Schedule a re-render after the keypress highlight fades
	fe.scheduleKeypressClear()

	fe.Compo.Update()
	return true
}

// handleFormKey forwards a key event to the active huh.Form.
func (fe *frontendPretty) handleFormKey(ev uv.KeyPressEvent) {
	msg := uvKeyToTeaKeyMsg(ev)
	form, cmd := fe.form.Update(msg)
	if f, ok := form.(*huh.Form); ok {
		fe.form = f
	}
	fe.execTeaCmd(cmd)
}

// handleEditlineKeyUV handles key events when the editline is focused.
func (fe *frontendPretty) handleEditlineKeyUV(ev uv.KeyPressEvent) {
	k := uv.Key(ev)
	keyStr := uvKeyString(k)
	fe.pressedKey = keyStr
	fe.pressedKeyAt = time.Now()

	switch keyStr {
	case "ctrl+d":
		if fe.editline.Value() == "" {
			fe.quitAction(ErrShellExited)
			return
		}
	case "ctrl+c":
		if fe.shellInterrupt != nil {
			fe.shellInterrupt(errors.New("interrupted"))
		}
		fe.editline.Reset()
		fe.updatePrompt()
		return
	case "ctrl+l":
		fe.tui.RequestRender(true)
		fe.updatePrompt()
		return
	case "esc":
		fe.enterNavMode(false)
		fe.updatePrompt()
		return
	case "alt++", "alt+=":
		fe.Verbosity++
		fe.renderVersion++
		fe.recalculateViewLocked()
		fe.updatePrompt()
		return
	case "alt+-":
		fe.Verbosity--
		fe.renderVersion++
		fe.recalculateViewLocked()
		fe.updatePrompt()
		return
	default:
		if fe.shell != nil {
			msg := uvKeyToTeaKeyMsg(ev)
			if work := fe.shell.ReactToInput(fe.shellCtx, msg, true, fe.editline); work != nil {
				fe.runShellAsync(work)
				return
			}
		}
	}

	// Forward to editline
	msg := uvKeyToTeaKeyMsg(ev)
	el, cmd := fe.editline.Update(msg)
	fe.editline = el.(*editline.Model)
	fe.execTeaCmd(cmd)

	// Check for input completion (editline sends InputCompleteMsg via its Cmd)
	// We handle it explicitly here since we can't route internal messages.
	fe.updatePrompt()
}

// handleNavKeyUV handles key events in navigation mode.
//
//nolint:gocyclo // splitting this up doesn't feel more readable
func (fe *frontendPretty) handleNavKeyUV(ev uv.KeyPressEvent) {
	k := uv.Key(ev)
	keyStr := uvKeyString(k)
	lastKey := fe.pressedKey
	fe.pressedKey = keyStr
	fe.pressedKeyAt = time.Now()

	switch keyStr {
	case "q", "ctrl+c":
		if fe.shell != nil {
			if fe.shellInterrupt != nil {
				fe.shellInterrupt(errors.New("interrupted"))
			}
		} else {
			fe.quitAction(ErrInterrupted)
		}
	case "ctrl+\\": // SIGQUIT
		// Note: can't release terminal mid-render in tuist the way bubbletea can.
		// Just send the signal.
		sigquit()
		return
	case "E":
		fe.NoExit = !fe.NoExit
		return
	case "down", "j":
		fe.goDown()
		return
	case "up", "k":
		fe.goUp()
		return
	case "left", "h":
		fe.closeOrGoOut()
		return
	case "right", "l":
		fe.openOrGoIn()
		return
	case "home":
		fe.goStart()
		return
	case "end", "G", " ":
		fe.goEnd()
		fe.pressedKey = "end"
		fe.pressedKeyAt = time.Now()
		return
	case "r":
		fe.goErrorOrigin()
		return
	case "esc":
		fe.ZoomedSpan = fe.db.PrimarySpan
		fe.renderVersion++
		fe.recalculateViewLocked()
		return
	case "+", "=":
		fe.FrontendOpts.Verbosity++
		fe.renderVersion++
		fe.recalculateViewLocked()
		return
	case "-":
		fe.FrontendOpts.Verbosity--
		if fe.FrontendOpts.Verbosity < -1 {
			fe.FrontendOpts.Verbosity = -1
		}
		fe.renderVersion++
		fe.recalculateViewLocked()
		return
	case "w":
		if fe.cloudURL == "" {
			return
		}
		url := fe.cloudURL
		if fe.ZoomedSpan.IsValid() && fe.ZoomedSpan != fe.db.PrimarySpan {
			url += "?span=" + fe.ZoomedSpan.String()
		}
		if fe.FocusedSpan.IsValid() && fe.FocusedSpan != fe.db.PrimarySpan {
			url += "#" + fe.FocusedSpan.String()
		}
		go func() {
			if err := browser.OpenURL(url); err != nil {
				slog.Warn("failed to open URL",
					"url", url,
					"err", err,
					"output", fe.browserBuf.String())
			}
		}()
		return
	case "?":
		if fe.debugged == fe.FocusedSpan {
			fe.debugged = dagui.SpanID{}
		} else {
			fe.debugged = fe.FocusedSpan
		}
		return
	case "enter":
		fe.ZoomedSpan = fe.FocusedSpan
		fe.renderVersion++
		fe.recalculateViewLocked()
		return
	case "tab", "i":
		fe.enterInsertMode(false)
		return
	case "t":
		fe.terminal()
		return
	default:
		if fe.shell != nil {
			msg := uvKeyToTeaKeyMsg(ev)
			if work := fe.shell.ReactToInput(fe.shellCtx, msg, false, fe.editline); work != nil {
				fe.runShellAsync(work)
				return
			}
		}
	}

	switch lastKey { //nolint:gocritic
	case "g":
		switch keyStr { //nolint:gocritic
		case "g":
			fe.goStart()
			fe.pressedKey = "home"
			fe.pressedKeyAt = time.Now()
			return
		}
	}
}

// ---------- editline input completion ---------------------------------------

// handleInputComplete is called when the editline signals that input is
// complete (user pressed Enter on a complete line).
func (fe *frontendPretty) handleInputComplete() {
	if !fe.editlineFocused {
		return
	}

	// reset prompt error state
	fe.promptErr = nil

	value := fe.editline.Value()
	fe.editline.AddHistoryEntry(value)
	fe.promptFg = termenv.ANSIYellow
	fe.updatePrompt()

	// reset now that we've accepted input
	fe.editline.Reset()
	if fe.shell != nil {
		ctx, cancel := context.WithCancelCause(fe.shellCtx)
		fe.shellInterrupt = cancel
		fe.shellRunning = true

		// switch back to following the bottom and re-enter nav mode
		fe.goEnd()
		fe.enterNavMode(true)

		go func() {
			fe.shellLock.Lock()
			defer fe.shellLock.Unlock()
			err := fe.shell.Handle(ctx, value)
			fe.tui.Dispatch(func() {
				fe.handleShellDone(err)
				fe.Compo.Update()
			})
		}()
	}
}

func (fe *frontendPretty) handleShellDone(err error) {
	// show error result above the prompt
	fe.promptErr = err
	if err == nil {
		fe.promptFg = termenv.ANSIGreen
	} else {
		fe.promptFg = termenv.ANSIRed
	}
	if fe.autoModeSwitch {
		fe.enterInsertMode(true)
	}
	fe.updatePrompt()
	fe.shellRunning = false
}

// ---------- tea.Cmd executor ------------------------------------------------

// execTeaCmd runs a bubbletea Cmd asynchronously and feeds the resulting
// message back through the TUI's dispatch loop. This bridges the bubbletea
// command model used by editline, huh.Form, and ShellHandler with the
// tuist dispatch model.
func (fe *frontendPretty) execTeaCmd(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	go func() {
		msg := cmd()
		if msg == nil {
			return
		}
		fe.tui.Dispatch(func() {
			fe.handleTeaMsg(msg)
			fe.Compo.Update()
		})
	}()
}

// handleTeaMsg routes a bubbletea message to the appropriate handler.
func (fe *frontendPretty) handleTeaMsg(msg tea.Msg) {
	switch msg := msg.(type) {
	case tea.QuitMsg:
		fe.doQuit()
	case tea.BatchMsg:
		for _, cmd := range msg {
			fe.execTeaCmd(cmd)
		}
	case editline.InputCompleteMsg:
		fe.handleInputComplete()
	case promptDone:
		fe.form = nil
	default:
		// Forward to form if active
		if fe.form != nil {
			form, cmd := fe.form.Update(msg)
			if f, ok := form.(*huh.Form); ok {
				fe.form = f
			}
			fe.execTeaCmd(cmd)
		}
		// Forward to editline if active
		if fe.editline != nil {
			el, cmd := fe.editline.Update(msg)
			fe.editline = el.(*editline.Model)
			fe.execTeaCmd(cmd)
		}
	}
}

// ---------- UV key conversion -----------------------------------------------

// uvKeyToTeaKeyMsg converts an ultraviolet KeyPressEvent to a bubbletea v1
// KeyMsg for forwarding to embedded bubbletea components (editline, huh.Form).
func uvKeyToTeaKeyMsg(ev uv.KeyPressEvent) tea.KeyMsg {
	k := uv.Key(ev)
	alt := k.Mod.Contains(uv.ModAlt)
	ctrl := k.Mod.Contains(uv.ModCtrl)
	shift := k.Mod.Contains(uv.ModShift)

	// Printable text (no ctrl modifier).
	if k.Text != "" && !ctrl {
		return tea.KeyMsg{
			Type:  tea.KeyRunes,
			Runes: []rune(k.Text),
			Alt:   alt,
		}
	}

	// Map special keys.
	keyType, ok := uvToV1Key[k.Code]
	if ok {
		if shifted, ok := shiftedKey(keyType, ctrl, shift); ok {
			return tea.KeyMsg{Type: shifted, Alt: alt}
		}
		return tea.KeyMsg{Type: keyType, Alt: alt}
	}

	// Ctrl+letter: bubbletea v1 maps ctrl+a to KeyCtrlA (0x01), etc.
	if ctrl && k.Code >= 'a' && k.Code <= 'z' {
		return tea.KeyMsg{Type: tea.KeyType(k.Code - 'a' + 1), Alt: alt}
	}

	// Printable rune fallback.
	if k.Code >= 0x20 {
		return tea.KeyMsg{
			Type:  tea.KeyRunes,
			Runes: []rune{k.Code},
			Alt:   alt,
		}
	}

	return tea.KeyMsg{Type: tea.KeyRunes}
}

var uvToV1Key = map[rune]tea.KeyType{
	uv.KeyUp:        tea.KeyUp,
	uv.KeyDown:      tea.KeyDown,
	uv.KeyLeft:      tea.KeyLeft,
	uv.KeyRight:     tea.KeyRight,
	uv.KeyHome:      tea.KeyHome,
	uv.KeyEnd:       tea.KeyEnd,
	uv.KeyPgUp:      tea.KeyPgUp,
	uv.KeyPgDown:    tea.KeyPgDown,
	uv.KeyDelete:    tea.KeyDelete,
	uv.KeyInsert:    tea.KeyInsert,
	uv.KeyTab:       tea.KeyTab,
	uv.KeyBackspace: tea.KeyBackspace,
	uv.KeyEnter:     tea.KeyEnter,
	uv.KeyEscape:    tea.KeyEscape,
	uv.KeySpace:     tea.KeySpace,
	uv.KeyF1:        tea.KeyF1,
	uv.KeyF2:        tea.KeyF2,
	uv.KeyF3:        tea.KeyF3,
	uv.KeyF4:        tea.KeyF4,
	uv.KeyF5:        tea.KeyF5,
	uv.KeyF6:        tea.KeyF6,
	uv.KeyF7:        tea.KeyF7,
	uv.KeyF8:        tea.KeyF8,
	uv.KeyF9:        tea.KeyF9,
	uv.KeyF10:       tea.KeyF10,
	uv.KeyF11:       tea.KeyF11,
	uv.KeyF12:       tea.KeyF12,
}

// shiftedKey returns the ctrl/shift variant of a base key type, if one exists
// in bubbletea v1's key model.
func shiftedKey(base tea.KeyType, ctrl, shift bool) (tea.KeyType, bool) {
	switch {
	case ctrl && shift:
		if k, ok := ctrlShiftKeys[base]; ok {
			return k, true
		}
	case ctrl:
		if k, ok := ctrlKeys[base]; ok {
			return k, true
		}
	case shift:
		if k, ok := shiftKeys[base]; ok {
			return k, true
		}
	}
	return 0, false
}

var ctrlKeys = map[tea.KeyType]tea.KeyType{
	tea.KeyUp:     tea.KeyCtrlUp,
	tea.KeyDown:   tea.KeyCtrlDown,
	tea.KeyLeft:   tea.KeyCtrlLeft,
	tea.KeyRight:  tea.KeyCtrlRight,
	tea.KeyHome:   tea.KeyCtrlHome,
	tea.KeyEnd:    tea.KeyCtrlEnd,
	tea.KeyPgUp:   tea.KeyCtrlPgUp,
	tea.KeyPgDown: tea.KeyCtrlPgDown,
}

var shiftKeys = map[tea.KeyType]tea.KeyType{
	tea.KeyUp:    tea.KeyShiftUp,
	tea.KeyDown:  tea.KeyShiftDown,
	tea.KeyLeft:  tea.KeyShiftLeft,
	tea.KeyRight: tea.KeyShiftRight,
	tea.KeyHome:  tea.KeyShiftHome,
	tea.KeyEnd:   tea.KeyShiftEnd,
	tea.KeyTab:   tea.KeyShiftTab,
}

var ctrlShiftKeys = map[tea.KeyType]tea.KeyType{
	tea.KeyUp:    tea.KeyCtrlShiftUp,
	tea.KeyDown:  tea.KeyCtrlShiftDown,
	tea.KeyLeft:  tea.KeyCtrlShiftLeft,
	tea.KeyRight: tea.KeyCtrlShiftRight,
	tea.KeyHome:  tea.KeyCtrlShiftHome,
	tea.KeyEnd:   tea.KeyCtrlShiftEnd,
}

// uvKeyString converts a uv.Key to the same string format as tea.KeyMsg.String()
// for compatibility with key.Binding comparison and pressedKey tracking.
func uvKeyString(k uv.Key) string {
	msg := uvKeyToTeaKeyMsg(uv.KeyPressEvent(k))
	return msg.String()
}

// ---------- mode switching --------------------------------------------------

func (fe *frontendPretty) enterNavMode(auto bool) {
	fe.autoModeSwitch = auto
	fe.editlineFocused = false
	fe.editline.Blur()
}

func (fe *frontendPretty) enterInsertMode(auto bool) {
	fe.autoModeSwitch = auto
	if fe.editline != nil {
		fe.editlineFocused = true
		fe.updatePrompt()
		fe.execTeaCmd(fe.editline.Focus())
	}
}

func (fe *frontendPretty) terminal() {
	if !fe.FocusedSpan.IsValid() {
		return
	}
	focused := fe.db.Spans.Map[fe.FocusedSpan]
	if focused == nil {
		return
	}

	callback := fe.terminalCallback(focused)
	if callback != nil {
		go func() {
			err := callback()
			if err != nil {
				slog.Error("failed to open terminal for span", err)
			}
		}()
	}
}

func (fe *frontendPretty) terminalCallback(span *dagui.Span) func() error {
	if fe.dag == nil {
		// we haven't got a dag client, so can't open a terminal
		return nil
	}

	// NOTE: this func is in the hot-path, so just use the call info to
	// determine if we can create a callback - the actual callback can do the
	// expensive id reconstruction
	call := span.Call()
	if call == nil {
		return nil
	}

	switch call.Type.NamedType {
	case "Container":
		if span.IsRunning() {
			break
		}
		return func() error {
			id, err := loadIDFromSpan(span)
			if err != nil {
				return err
			}
			_, err = fe.dag.LoadContainerFromID(dagger.ContainerID(id)).Terminal().Sync(fe.runCtx)
			return err
		}
	case "Directory":
		if span.IsRunning() {
			break
		}
		return func() error {
			id, err := loadIDFromSpan(span)
			if err != nil {
				return err
			}
			_, err = fe.dag.LoadDirectoryFromID(dagger.DirectoryID(id)).Terminal().Sync(fe.runCtx)
			return err
		}
	case "Service":
		return func() error {
			id, err := loadIDFromSpan(span)
			if err != nil {
				return err
			}
			_, err = fe.dag.LoadServiceFromID(dagger.ServiceID(id)).Terminal().Sync(fe.runCtx)
			return err
		}
	}

	return nil
}

func loadIDFromSpan(span *dagui.Span) (string, error) {
	callID, err := span.CallID()
	if err != nil {
		return "", err
	}
	id, err := callID.Encode()
	if err != nil {
		return "", err
	}
	return id, nil
}

func (fe *frontendPretty) initEditline() {
	// create the editline
	fe.editline = editline.New(fe.contentWidth, fe.window.Height)
	fe.editline.HideKeyMap = true
	fe.editlineFocused = true
	// HACK: for some reason editline's first paint is broken (only shows
	// first 2 chars of prompt, doesn't show cursor). Sending it a message
	// - any message - fixes it.
	fe.editline.Update(nil)
}

func (fe *frontendPretty) updatePrompt() {
	if fe.shell != nil && fe.editline != nil {
		promptOut := NewOutput(io.Discard, termenv.WithProfile(fe.profile))
		prompt, init := fe.shell.Prompt(fe.runCtx, promptOut, fe.promptFg)
		fe.editline.Prompt = prompt
		if init != nil {
			fe.runShellAsync(init)
		}
	}
	if fe.editline != nil {
		fe.editline.UpdatePrompt()
	}
}

// runShellAsync runs a shell handler function in a background goroutine,
// then dispatches a prompt update + re-render back to the UI thread.
func (fe *frontendPretty) runShellAsync(work func()) {
	fe.updatePrompt()
	go func() {
		work()
		fe.tui.Dispatch(func() {
			fe.updatePrompt()
			fe.Compo.Update()
		})
	}()
}

func (fe *frontendPretty) quitAction(interruptErr error) {
	if fe.cleanup != nil {
		cleanup := fe.cleanup
		fe.cleanup = nil // prevent double cleanup
		go func() {
			cleanup()
			fe.tui.Dispatch(func() {
				fe.quitting = true
				fe.doQuit()
			})
		}()
	} else if fe.interrupted {
		slog.Warn("exiting immediately")
		fe.quitting = true
		fe.doQuit()
	} else {
		slog.Warn("canceling... (press again to exit immediately)")
		fe.interrupted = true
		fe.interrupt(interruptErr)
	}
}

func (fe *frontendPretty) goStart() {
	fe.autoFocus = false
	if len(fe.rows.Order) > 0 {
		fe.focus(fe.rows.Order[0])
	}
}

func (fe *frontendPretty) goEnd() {
	fe.autoFocus = true
	if len(fe.rows.Order) > 0 {
		fe.focus(fe.rows.Order[len(fe.rows.Order)-1])
	}
}

func (fe *frontendPretty) goUp() {
	fe.autoFocus = false
	newIdx := fe.focusedIdx - 1
	if newIdx < 0 || newIdx >= len(fe.rows.Order) {
		return
	}
	fe.focus(fe.rows.Order[newIdx])
}

func (fe *frontendPretty) goDown() {
	fe.autoFocus = false
	newIdx := fe.focusedIdx + 1
	if newIdx >= len(fe.rows.Order) {
		// at bottom
		return
	}
	fe.focus(fe.rows.Order[newIdx])
}

func (fe *frontendPretty) goOut() {
	fe.autoFocus = false
	focused := fe.rows.BySpan[fe.FocusedSpan]
	if focused == nil {
		return
	}
	parent := focused.Parent
	if parent == nil {
		return
	}
	fe.FocusedSpan = parent.Span.ID
	fe.recalculateViewLocked()
}

func (fe *frontendPretty) goIn() {
	fe.autoFocus = false
	newIdx := fe.focusedIdx + 1
	if newIdx >= len(fe.rows.Order) {
		// at bottom
		return
	}
	cur := fe.rows.Order[fe.focusedIdx]
	next := fe.rows.Order[newIdx]
	if next.Depth <= cur.Depth {
		// has no children
		return
	}
	fe.focus(next)
}

func (fe *frontendPretty) closeOrGoOut() {
	// Only toggle if we have a valid focused span
	if fe.FocusedSpan.IsValid() {
		// Get the either explicitly set or defaulted value
		var isExpanded bool
		if row, ok := fe.rows.BySpan[fe.FocusedSpan]; ok {
			isExpanded = row.Expanded
		}
		if !isExpanded {
			// already closed; move up
			fe.goOut()
			return
		}
		// Toggle the expanded state for the focused span
		fe.setExpanded(fe.FocusedSpan, !isExpanded)
		// Recalculate view to reflect changes
		fe.recalculateViewLocked()
	}
}

func (fe *frontendPretty) openOrGoIn() {
	// Only toggle if we have a valid focused span
	if fe.FocusedSpan.IsValid() {
		// Get the either explicitly set or defaulted value
		var isExpanded bool
		if row, ok := fe.rows.BySpan[fe.FocusedSpan]; ok {
			isExpanded = row.Expanded
		}
		if isExpanded {
			// already expanded; go in
			fe.goIn()
			return
		}
		// Toggle the expanded state for the focused span
		fe.setExpanded(fe.FocusedSpan, true)
		// Recalculate view to reflect changes
		fe.recalculateViewLocked()
	}
}

func (fe *frontendPretty) goErrorOrigin() {
	fe.autoFocus = false
	focused := fe.db.Spans.Map[fe.FocusedSpan]
	if focused == nil {
		return
	}
	if len(focused.ErrorOrigins.Order) == 0 {
		return
	}
	fe.FocusedSpan = focused.ErrorOrigins.Order[0].ID // TODO which?
	focusedRow := fe.rowsView.BySpan[fe.FocusedSpan]
	if focusedRow == nil {
		return
	}
	for cur := focusedRow.Parent; cur != nil; cur = cur.Parent {
		// expand parents of target span
		fe.setExpanded(cur.Span.ID, true)
	}
	fe.recalculateViewLocked()
}



func (fe *frontendPretty) setWindowSizeLocked(msg windowSize) {
	fe.window = msg
	fe.contentWidth = msg.Width
	fe.logs.SetWidth(fe.contentWidth)
	if fe.editline != nil {
		fe.editline.SetSize(fe.contentWidth, msg.Height)
		fe.editline.Update(nil) // bleh
	}
}

func (fe *frontendPretty) setExpanded(id dagui.SpanID, expanded bool) {
	if fe.SpanExpanded == nil {
		fe.SpanExpanded = make(map[dagui.SpanID]bool)
	}
	fe.SpanExpanded[id] = expanded
	fe.recalculateViewLocked()
}

// renderProgressFinal renders all rows using direct rendering (no component
// caching). Used by FinalRender after the TUI has stopped.
func (fe *frontendPretty) renderProgressFinal(out TermOutput, r *renderer) {
	if fe.rowsView == nil {
		return
	}
	for _, row := range fe.rows.Order {
		fe.renderRow(out, r, row, "")
	}
}

// renderRowGap renders the gap line(s) that precede a row, based on the
// relationship with the previous visual row. Returns the gap lines (if any).
// This is intentionally NOT part of SpanTreeView so that gap changes don't
// require re-rendering the row's content.
func (fe *frontendPretty) renderRowGap(r *renderer, row *dagui.TraceRow, prefix string) []string {
	if fe.shell != nil {
		if row.Depth == 0 && row.Previous != nil {
			return []string{prefix}
		}
		return nil
	}
	if row.PreviousVisual != nil &&
		row.PreviousVisual.Depth >= row.Depth &&
		!row.Chained &&
		( // ensure gaps after last nested child
		row.PreviousVisual.Depth > row.Depth ||
			// ensure gaps before unchained calls
			row.Span.Call() != nil ||
			// ensure gaps before checks
			row.Span.CheckName != "" ||
			// ensure gaps before generators
			row.Span.GeneratorName != "" ||
			// ensure gaps between calls and non-calls
			(row.PreviousVisual.Span.Call() != nil && row.Span.Call() == nil) ||
			// ensure gaps between messages
			(row.PreviousVisual.Span.Message != "" && row.Span.Message != "") ||
			// ensure gaps going from tool calls to messages
			(row.PreviousVisual.Span.Message == "" && row.Span.Message != "")) {
		buf := new(strings.Builder)
		out := NewOutput(buf, termenv.WithProfile(fe.profile))
		fmt.Fprint(out, prefix)
		r.fancyIndent(out, row.PreviousVisual, false, false)
		return []string{buf.String()}
	}
	return nil
}

// renderRow renders a full row including gap lines. Used by the final render
// path which doesn't use per-span caching.
func (fe *frontendPretty) renderRow(out TermOutput, r *renderer, row *dagui.TraceRow, prefix string) bool { //nolint: gocyclo
	for _, gap := range fe.renderRowGap(r, row, prefix) {
		fmt.Fprintln(out, gap)
	}
	fe.renderRowContent(out, r, row, prefix)
	return true
}

// renderRowContent renders the actual content of a row (step + logs + errors
// + debug) without gap lines. This is what SpanTreeView.Render() calls.
func (fe *frontendPretty) renderRowContent(out TermOutput, r *renderer, row *dagui.TraceRow, prefix string) {
	span := row.Span
	isFocused := span.ID == fe.FocusedSpan && !fe.editlineFocused
	fe.renderStep(out, r, row, prefix)

	if span.Message == "" && // messages are displayed in renderStep
		(row.Expanded || row.Span.LLMTool != "") {
		fe.renderStepLogs(out, r, row, prefix, isFocused)
	} else if (row.Span.RollUpLogs || fe.shell != nil) && row.Depth == 0 && !row.Expanded {
		// in shell mode, we print top-level command logs unindented, like shells
		// usually does
		if logs := fe.logs.Logs[row.Span.ID]; logs != nil && logs.UsedHeight() > 0 {
			if fe.shell != nil {
				unindent := *row
				unindent.Depth = -1
				fe.renderLogs(out, r, &unindent, logs, logs.UsedHeight(), prefix, false)
			} else if row.Span.RollUpLogs && row.IsRunningOrChildRunning {
				// Only show rolled-up logs while the span is running.
				fe.renderStepLogs(out, r, row, prefix, isFocused)
			}
		}
	}
	if len(row.Span.ErrorOrigins.Order) > 0 && (!row.Expanded || !row.HasChildren) {
		multi := len(row.Span.ErrorOrigins.Order) > 1
		for _, cause := range row.Span.ErrorOrigins.Order {
			if multi {
				r.fancyIndent(out, row, false, false)
				fmt.Fprintln(out, prefix)
			}
			fe.renderErrorCause(out, r, row, prefix, cause)
		}
	} else {
		fe.renderStepError(out, r, row, prefix)
	}
	fe.renderDebug(out, row.Span, prefix+Block25+" ", false)
}

func (fe *frontendPretty) renderDebug(out TermOutput, span *dagui.Span, prefix string, force bool) {
	if span.ID != fe.debugged && !force {
		return
	}
	vt := NewVterm(fe.profile)
	vt.WriteMarkdown([]byte("## Span\n"))
	vt.SetPrefix(prefix)
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.Encode(span.Snapshot())
	vt.WriteMarkdown([]byte("```json\n" + strings.TrimSpace(buf.String()) + "\n```"))
	if len(span.EffectIDs) > 0 {
		vt.WriteMarkdown([]byte("\n\n## Installed effects\n\n"))
		for _, id := range span.EffectIDs {
			vt.WriteMarkdown([]byte("- " + id + "\n"))
			if spans := fe.db.EffectSpans[id]; spans != nil {
				for _, effect := range spans.Order {
					vt.WriteMarkdown([]byte("  - " + effect.Name + "\n"))
				}
			}
		}
	}
	if len(span.RevealedSpans.Order) > 0 {
		vt.WriteMarkdown([]byte("\n\n## Revealed spans\n\n"))
		for _, revealed := range span.RevealedSpans.Order {
			vt.WriteMarkdown([]byte("- " + revealed.Name + "\n"))
		}
	}
	if len(span.ErrorOrigins.Order) > 0 {
		vt.WriteMarkdown([]byte("\n\n## Error origins\n\n"))
		for _, span := range span.ErrorOrigins.Order {
			vt.WriteMarkdown([]byte("- " + span.Name + "\n"))
		}
	}
	fmt.Fprint(out, prefix+vt.View())
}

// sync this with core.llmLogsLastLines to ensure user and LLM sees the same
// thing
const llmLogsLastLines = 8

func (fe *frontendPretty) renderStepLogs(out TermOutput, r *renderer, row *dagui.TraceRow, prefix string, focused bool) bool {
	limit := fe.window.Height / 3
	if row.Span.LLMTool != "" && !row.Expanded {
		limit = llmLogsLastLines
	}
	if logs := fe.logs.Logs[row.Span.ID]; logs != nil {
		return fe.renderLogs(out, r, row, logs, limit, prefix, focused)
	}
	return false
}

func (fe *frontendPretty) renderErrorCause(out TermOutput, r *renderer, row *dagui.TraceRow, prefix string, rootCause *dagui.Span) {
	rootCauseTree := fe.rowsView.BySpan[rootCause.ID]
	if rootCauseTree == nil {
		// error origin has no tree, likely due to internal/hidden spans
		// create a synthetic tree by walking span parents
		var syntheticParents []*dagui.Span
		for current := rootCause; current != nil && current.ParentID.IsValid(); {
			parent := fe.db.Spans.Map[current.ParentID]
			if parent == nil {
				break
			}
			syntheticParents = append(syntheticParents, parent)
			current = parent
			// Stop if we reach the current row's span or a boundary
			if parent.ID == row.Span.ID {
				break
			}
		}

		// Create synthetic tree structure
		rootCauseTree = &dagui.TraceTree{
			Span: rootCause,
		}

		// Build parent chain
		current := rootCauseTree
		for i := len(syntheticParents) - 1; i >= 0; i-- {
			parent := &dagui.TraceTree{
				Span: syntheticParents[i],
			}
			current.Parent = parent
			current = parent
		}
	}

	rootCauseRow := &dagui.TraceRow{
		Span:     rootCause,
		Chained:  false,
		Expanded: true,
		Depth:    row.Depth,
	}

	var parents []*dagui.TraceRow
	for p := rootCauseTree.Parent; p != nil; p = p.Parent {
		if p.Span.ID == row.Span.ID {
			break
		}
		parentRow := &dagui.TraceRow{
			Span:     p.Span,
			Chained:  p.Chained,
			Depth:    row.Depth,
			Expanded: true,
		}
		parents = append(parents, parentRow)
	}

	indent := strings.Repeat("  ", row.Depth)
	if !fe.finalRender {
		indent += "  "
	}

	indentBuf := new(strings.Builder)
	fmt.Fprint(indentBuf, prefix)
	indentOut := NewOutput(indentBuf, termenv.WithProfile(fe.profile))
	r.fancyIndent(indentOut, row, false, false)
	if !fe.finalRender {
		fmt.Fprint(indentOut, "  ")
	}

	if len(parents) > 0 {
		r.fancyIndent(out, row, false, false)
		if !fe.finalRender {
			fmt.Fprint(out, "  ")
		}
		slices.Reverse(parents)
		context := new(strings.Builder)
		noColorOut := termenv.NewOutput(context, termenv.WithProfile(termenv.Ascii))
		fmt.Fprint(noColorOut, VertBoldDash3+" ")
		for _, p := range parents {
			fe.renderStepTitle(noColorOut, r, p, prefix+indent, true)
			fmt.Fprintf(noColorOut, " › ")
		}
		fmt.Fprint(out, out.String(context.String()).Foreground(termenv.ANSIBrightBlack).Faint())
		fmt.Fprintln(out)
	}
	r.fancyIndent(out, row, false, false)
	if !fe.finalRender {
		fmt.Fprint(out, "  ")
	}
	fe.renderStepTitle(out, r, rootCauseRow, prefix+indent, false)
	fmt.Fprintln(out)
	if logs := fe.logs.Logs[rootCauseRow.Span.ID]; logs != nil {
		if row.Depth == 0 && fe.finalRender {
			logs.SetPrefix("")
		} else {
			pipe := out.String(VertBoldBar).Foreground(restrainedStatusColor(rootCauseRow.Span)).String()
			logs.SetPrefix(indentBuf.String() + pipe + " ")
		}
		if fe.finalRender {
			logs.SetHeight(logs.UsedHeight())
		} else {
			logs.SetHeight(fe.window.Height / 3)
		}
		fmt.Fprint(out, logs.View())
	}
	fe.renderStepError(out, r, rootCauseRow, indentBuf.String())

	fe.shownErrs[rootCause.ID] = true
}

func (fe *frontendPretty) hasShownRootError() bool {
	if fe.err == nil {
		return false
	}
	origins := telemetry.ParseErrorOrigins(fe.err.Error())
	if len(origins) == 0 {
		return false
	}
	for _, origin := range origins {
		if !origin.IsValid() {
			return false
		}
		if !fe.shownErrs[dagui.SpanID{SpanID: origin.SpanID()}] {
			return false
		}
	}
	return true
}

func (fe *frontendPretty) renderStepError(out TermOutput, r *renderer, row *dagui.TraceRow, prefix string) {
	if len(row.Span.ErrorOrigins.Order) > 0 {
		return
	}
	fe.shownErrs[row.Span.ID] = true
	errorCounts := map[string]int{}
	for _, span := range row.Span.Errors().Order {
		errText := span.Status.Description
		if errText == "" {
			continue
		}
		errorCounts[errText]++
	}
	type errWithCount struct {
		text  string
		count int
	}
	var counts []errWithCount
	for errText, count := range errorCounts {
		counts = append(counts, errWithCount{errText, count})
	}
	sort.Slice(counts, func(i, j int) bool {
		if counts[i].count == counts[j].count {
			return counts[i].text < counts[j].text
		}
		return counts[i].count > counts[j].count
	})
	for _, c := range counts {
		errText, count := c.text, c.count
		prefixWidth := lipgloss.Width(prefix)
		indentWidth := row.Depth * 2
		markerWidth := 2
		availableWidth := fe.contentWidth - prefixWidth - indentWidth - markerWidth
		if availableWidth > 0 {
			errText = cellbuf.Wrap(errText, availableWidth, "")
		}

		if count > 1 {
			errText = fmt.Sprintf("%dx ", count) + errText
		}

		first := true
		for line := range strings.SplitSeq(strings.TrimSpace(errText), "\n") {
			fmt.Fprint(out, prefix)
			r.fancyIndent(out, row, false, false)
			var symbol string
			if first {
				symbol = "!"
			} else {
				symbol = " "
			}
			fmt.Fprintf(out,
				out.String("%s %s").Foreground(termenv.ANSIRed).String(),
				symbol,
				line,
			)
			fmt.Fprintln(out)
			first = false
		}
	}
}

func (fe *frontendPretty) renderStepTitle(out TermOutput, r *renderer, row *dagui.TraceRow, prefix string, abridged bool) error {
	span := row.Span
	chained := row.Chained
	depth := row.Depth
	isFocused := span.ID == fe.FocusedSpan && !fe.editlineFocused && fe.form == nil

	if !abridged && row.Span.LLMRole == "" {
		fe.renderStatusIcon(out, row)
		fmt.Fprint(out, " ")
	}

	if r.Debug {
		fmt.Fprintf(out, out.String("%s ").Foreground(termenv.ANSIBrightBlack).String(), span.ID)
	}

	var empty bool
	if span.Message != "" {
		if fe.renderStepLogs(out, r, row, prefix, isFocused) {
			if span.LLMRole == telemetry.LLMRoleUser {
				return nil
			}
			r.fancyIndent(out, row, false, false)
			bar := out.String(VertBoldBar).Foreground(restrainedStatusColor(span))
			if isFocused {
				bar = hl(bar)
			}
			fmt.Fprint(out, bar)
		} else {
			empty = true
		}
	} else if call := span.Call(); call != nil {
		if err := r.renderCall(out, span, call, prefix, chained, depth, span.Internal, row, abridged); err != nil {
			return err
		}
	} else if span != nil {
		if span.Name == "" {
			empty = true
		}
		if err := r.renderSpan(out, span, span.Name); err != nil {
			return err
		}
	}

	if span != nil && !abridged {
		r.renderDuration(out, span, !empty)

		if span.RollUpSpans {
			dots := fe.renderRollUpDots(out, span, row, prefix, fe.FrontendOpts)
			if dots != "" {
				fmt.Fprint(out, " ")
				fmt.Fprint(out, dots)
			}
		}

		fe.renderStatus(out, span)
		r.renderMetrics(out, span)

		summary := map[string]int{}
		for effect := range span.EffectSpans {
			if effect.Passthrough {
				continue
			}
			icon, isInteresting := fe.statusIcon(effect)
			if !isInteresting {
				summary[icon]++
				continue
			}
			fmt.Fprintf(out, " %s ", out.String(icon).Foreground(statusColor(effect)))
			r.renderSpan(out, effect, effect.Name)
		}

		for _, icon := range statusOrder {
			count := summary[icon]
			if count > 0 {
				color := statusColors[icon]
				fmt.Fprintf(out, " %s %s",
					out.String(icon).Foreground(color).Faint(),
					out.String(strconv.Itoa(count)).Faint())
			}
		}
	}

	return nil
}

func (fe *frontendPretty) renderStep(out TermOutput, r *renderer, row *dagui.TraceRow, prefix string) error {
	span := row.Span
	isFocused := span.ID == fe.FocusedSpan && !fe.editlineFocused && fe.form == nil

	fmt.Fprint(out, prefix)
	r.fancyIndent(out, row, false, true)

	if row.Span.LLMRole != "" {
		switch row.Span.LLMRole {
		case telemetry.LLMRoleUser:
			fmt.Fprint(out, out.String(Block).Foreground(termenv.ANSIMagenta))
		case telemetry.LLMRoleAssistant:
			fmt.Fprint(out, out.String(VertBoldBar).Foreground(termenv.ANSIMagenta))
		}
		fmt.Fprint(out, " ")
	} else if !fe.finalRender {
		fe.renderToggler(out, row, isFocused)
		fmt.Fprint(out, " ")
	}

	if err := fe.renderStepTitle(out, r, row, prefix, false); err != nil {
		return err
	}

	fmt.Fprintln(out)

	return nil
}

var statusOrder = []string{
	DotFilled,
	IconSuccess,
	IconCached,
	IconSkipped,
	DotEmpty,
}

var statusColors = map[string]termenv.Color{
	DotHalf:     termenv.ANSIYellow,
	IconCached:  termenv.ANSIBlue,
	IconSkipped: termenv.ANSIBrightBlack,
	IconFailure: termenv.ANSIRed,
	DotEmpty:    termenv.ANSIBrightBlack,
	DotFilled:   termenv.ANSIGreen,
	IconSuccess: termenv.ANSIGreen,
}

var brailleDots = []rune{
	' ',      // 0 dots
	'\u2840', // 1 dot
	'\u2844', // 2 dots
	'\u2846', // 3 dots
	'\u2847', // 4 dots
	'\u28C7', // 5 dots
	'\u28E7', // 6 dots
	'\u28F7', // 7 dots
	'\u28FF', // 8 dots
}

func (fe *frontendPretty) renderRollUpDots(out TermOutput, span *dagui.Span, row *dagui.TraceRow, prefix string, _ dagui.FrontendOpts) string {
	if !span.RollUpSpans {
		return ""
	}

	state := span.RollUpState()
	if state == nil {
		return ""
	}

	prefixWidth := lipgloss.Width(prefix)
	indentWidth := row.Depth * 2
	togglerWidth := 2
	nameWidth := lipgloss.Width(span.Name)
	extraWidth := 25
	usedWidth := prefixWidth + indentWidth + togglerWidth + nameWidth + extraWidth
	availableWidth := max(fe.contentWidth-usedWidth, 5)

	totalSpans := state.SuccessCount + state.CachedCount + state.FailedCount +
		state.CanceledCount + state.RunningCount + state.PendingCount

	if totalSpans == 0 {
		return ""
	}

	maxChars := availableWidth
	maxDots := maxChars * 8

	scale := 1
	for totalSpans/scale > maxDots {
		if scale < 5 {
			scale++
		} else {
			scale = (scale/5 + 1) * 5
		}
	}

	var result strings.Builder

	renderGroup := func(count int, color termenv.Color) {
		if count == 0 {
			return
		}
		dotCount := (count + scale - 1) / scale
		for i := 0; i < dotCount; i += 8 {
			dotsInChar := min(dotCount-i, 8)
			braille := string(brailleDots[dotsInChar])
			styled := out.String(braille).Foreground(color)
			result.WriteString(styled.String())
		}
	}

	if scale > 1 {
		scaleIndicator := fmt.Sprintf("%d×", scale)
		styled := out.String(scaleIndicator).Foreground(termenv.ANSIBrightBlack).Faint()
		result.WriteString(styled.String())
	}

	renderGroup(state.SuccessCount, termenv.ANSIGreen)
	renderGroup(state.CachedCount, termenv.ANSIBlue)
	renderGroup(state.FailedCount, termenv.ANSIRed)
	renderGroup(state.CanceledCount, termenv.ANSIBrightBlack)
	renderGroup(state.RunningCount, termenv.ANSIYellow)
	renderGroup(state.PendingCount, termenv.ANSIBrightBlack)

	return result.String()
}

func (fe *frontendPretty) statusIcon(span *dagui.Span) (string, bool) {
	if span.IsRunningOrEffectsRunning() {
		// Look up the per-span spinner for animation
		if sr, ok := fe.spanTrees[span.ID]; ok && sr.spinner != nil {
			return sr.spinner.ViewFancy(), true
		}
		// Fallback for effect spans or spans without a SpanTreeView
		return fe.spinner.ViewFancy(time.Now()), true
	} else if span.IsCached() {
		return IconCached, false
	} else if span.IsCanceled() {
		return IconSkipped, false
	} else if span.IsFailedOrCausedFailure() {
		return IconFailure, true
	} else if span.IsPending() {
		return DotEmpty, false
	} else {
		return IconSuccess, false
	}
}

func (fe *frontendPretty) renderToggler(out TermOutput, row *dagui.TraceRow, isFocused bool) {
	var icon termenv.Style
	if row.HasChildren || row.Span.HasLogs {
		if row.Expanded {
			icon = out.String(CaretDownFilled).Foreground(termenv.ANSIBrightBlack)
		} else {
			icon = out.String(CaretRightFilled).Foreground(termenv.ANSIBrightBlack)
		}
	} else {
		icon = out.String(DotFilled).Foreground(termenv.ANSIBrightBlack)
	}

	if isFocused {
		icon = hl(icon.Foreground(statusColor(row.Span)))
	}
	fmt.Fprint(out, icon.String())
}

func (fe *frontendPretty) renderStatusIcon(out TermOutput, row *dagui.TraceRow) {
	icon, _ := fe.statusIcon(row.Span)
	statusIcon := out.String(icon).Foreground(statusColor(row.Span))
	fmt.Fprint(out, statusIcon.String())
}

func (fe *frontendPretty) renderStatus(out TermOutput, span *dagui.Span) {
	if span.CheckPassed {
		fmt.Fprint(out, out.String(" "))
		fmt.Fprint(out, out.String("OK").Foreground(termenv.ANSIGreen))
	} else if span.IsFailedOrCausedFailure() && !span.IsCanceled() {
		fmt.Fprint(out, out.String(" "))
		fmt.Fprint(out, out.String("ERROR").Foreground(termenv.ANSIRed))
		if len(span.ErrorOrigins.Order) > 0 && !fe.reportOnly && !fe.finalRender {
			color := termenv.ANSIBrightBlack
			_, focusedAnyOrigin := span.ErrorOrigins.Map[fe.FocusedSpan]
			if time.Since(fe.pressedKeyAt) < keypressDuration && focusedAnyOrigin {
				color = termenv.ANSIWhite
			}
			fmt.Fprintf(out, " %s %s",
				out.String("r").Foreground(color).Bold(),
				out.String("jump ↴").Foreground(color),
			)
		}
	} else if !span.IsRunningOrEffectsRunning() && span.IsCached() {
		fmt.Fprint(out, out.String(" "))
		fmt.Fprint(out, out.String("CACHED").Foreground(termenv.ANSIBlue))
	}
}

func (fe *frontendPretty) renderLogs(out TermOutput, r *renderer, row *dagui.TraceRow, logs *Vterm, height int, prefix string, focused bool) bool {
	span := row.Span
	depth := row.Depth

	pipe := out.String(VertBoldBar).Foreground(restrainedStatusColor(span))
	dashed := out.String(VertBoldDash3).Foreground(restrainedStatusColor(span))
	if focused {
		pipe = hl(pipe)
		dashed = hl(dashed)
	}

	if depth == -1 {
		logs.SetPrefix(prefix)
	} else {
		pipeBuf := new(strings.Builder)
		fmt.Fprint(pipeBuf, prefix)
		indentOut := NewOutput(pipeBuf, termenv.WithProfile(fe.profile))
		r.fancyIndent(indentOut, row, false, false)
		fmt.Fprint(indentOut, pipe)
		fmt.Fprint(indentOut, out.String(" "))
		logs.SetPrefix(pipeBuf.String())
	}
	if height <= 0 {
		height = logs.UsedHeight()
	}
	trimmed := logs.UsedHeight() - height
	if trimmed > 0 {
		fmt.Fprint(out, prefix)
		r.fancyIndent(out, row, false, false)
		fmt.Fprint(out, dashed)
		fmt.Fprint(out, out.String(" "))
		fmt.Fprint(out, out.String("...").Foreground(termenv.ANSIBrightBlack))
		fmt.Fprintf(out, out.String("%d").Foreground(termenv.ANSIBrightBlack).Bold().String(), trimmed)
		fmt.Fprintln(out, out.String(" lines hidden...").Foreground(termenv.ANSIBrightBlack))
	}
	logs.SetHeight(height)
	view := logs.View()
	if view == "" {
		return false
	}
	fmt.Fprint(out, view)
	return true
}

func (fe *frontendPretty) logsDone(id dagui.SpanID, waitForLogs bool) bool {
	if fe.logs == nil {
		return true
	}
	if _, ok := fe.logs.Logs[id]; !ok && !waitForLogs {
		return true
	}
	return fe.logs.SawEOF[id]
}

func (fe *frontendPretty) editlineView() string {
	view := fe.editline.View()
	if fe.promptErr != nil {
		errOut := NewOutput(io.Discard, termenv.WithProfile(fe.profile))
		view = errOut.String("ERROR: "+fe.promptErr.Error()).Foreground(termenv.ANSIBrightRed).String() + "\n" + view
	}
	return view
}

func (fe *frontendPretty) formView() string {
	return fe.form.View() + "\n\n"
}

// ---------- pretty logs (unchanged) -----------------------------------------

type prettyLogs struct {
	DB            *dagui.DB
	Logs          map[dagui.SpanID]*Vterm
	PrefixWriters map[dagui.SpanID]*multiprefixw.Writer
	LogWidth      int
	SawEOF        map[dagui.SpanID]bool
	Profile       termenv.Profile
	Output        TermOutput
}

func newPrettyLogs(profile termenv.Profile, db *dagui.DB) *prettyLogs {
	return &prettyLogs{
		DB:            db,
		Logs:          make(map[dagui.SpanID]*Vterm),
		PrefixWriters: make(map[dagui.SpanID]*multiprefixw.Writer),
		LogWidth:      -1,
		Profile:       profile,
		SawEOF:        make(map[dagui.SpanID]bool),
		Output:        termenv.NewOutput(io.Discard, termenv.WithProfile(profile)),
	}
}

func (l *prettyLogs) Export(ctx context.Context, logs []sdklog.Record) error {
	for _, log := range logs {
		contentType := ""
		eof := false
		verbose := false
		global := false
		for attr := range log.WalkAttributes {
			switch attr.Key {
			case telemetry.ContentTypeAttr:
				contentType = attr.Value.AsString()
			case telemetry.StdioEOFAttr:
				eof = attr.Value.AsBool()
			case telemetry.LogsGlobalAttr:
				global = attr.Value.AsBool()
			case telemetry.LogsVerboseAttr:
				verbose = attr.Value.AsBool()
			}
		}

		if eof && log.SpanID().IsValid() {
			l.SawEOF[dagui.SpanID{SpanID: log.SpanID()}] = true
			continue
		}

		targetID := log.SpanID()

		spanID := dagui.SpanID{SpanID: targetID}
		pw, rolledUp := l.findRollUpSpan(spanID)
		if rolledUp && !verbose && !global {
			var context string
			span, ok := l.DB.Spans.Map[spanID]
			if ok {
				context = l.extractSpanContext(span)
			} else {
				context = targetID.String()
			}
			pw.Prefix = l.Output.String("["+context+"]").Foreground(termenv.ANSICyan).String() + " "
			fmt.Fprint(pw, log.Body().AsString())
		}

		vterm := l.spanLogs(spanID)
		if contentType == "text/markdown" {
			_, _ = vterm.WriteMarkdown([]byte(log.Body().AsString()))
		} else {
			_, _ = fmt.Fprint(vterm, log.Body().AsString())
		}
	}
	return nil
}

func (l *prettyLogs) extractSpanContext(span *dagui.Span) string {
	call := span.Call()
	if call == nil {
		return span.Name
	}

	if call.Field == "withExec" {
		if len(call.Args) > 0 && call.Args[0].Name == "args" {
			if argList := call.Args[0].Value.GetList(); argList != nil {
				if len(argList.Values) > 0 {
					cmd := argList.Values[0].GetString_()
					if cmd != "" {
						return cmd
					}
				}
			}
		}
		return "exec"
	}

	if call.Field != "" {
		return call.Field
	}

	return span.Name
}

func (l *prettyLogs) findRollUpSpan(origID dagui.SpanID) (*multiprefixw.Writer, bool) {
	id := origID
	for {
		span := l.DB.Spans.Map[id]
		if span == nil {
			break
		}
		if span.Boundary || span.Encapsulate || span.Internal {
			break
		}
		if span.RollUpLogs {
			pw, found := l.PrefixWriters[id]
			if !found {
				vterm := l.spanLogs(id)
				pw = multiprefixw.New(vterm)
				l.PrefixWriters[id] = pw
			}
			return pw, true
		}
		if span.ParentID.IsValid() {
			id = span.ParentID
		} else {
			break
		}
	}
	return nil, false
}

func (l *prettyLogs) spanLogs(spanID dagui.SpanID) *Vterm {
	term, found := l.Logs[spanID]
	if !found {
		term = NewVterm(l.Profile)
		if l.LogWidth > -1 {
			term.SetWidth(l.LogWidth)
		}
		l.Logs[spanID] = term
	}
	return term
}

func (l *prettyLogs) SetWidth(width int) {
	l.LogWidth = width
	for _, vt := range l.Logs {
		vt.SetWidth(width)
	}
}

func (l *prettyLogs) Shutdown(ctx context.Context) error {
	return nil
}

func findTTYs() (in io.Reader, out io.Writer) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		in = os.Stdin
	}
	for _, f := range []*os.File{os.Stderr, os.Stdout} {
		if term.IsTerminal(int(f.Fd())) {
			out = f
			break
		}
	}
	return
}

type wrapCommand struct {
	tea.ExecCommand
	before func() error
	after  func() error
}

var _ tea.ExecCommand = (*wrapCommand)(nil)

func (ts *wrapCommand) Run() error {
	if err := ts.before(); err != nil {
		return err
	}
	err := ts.ExecCommand.Run()
	if err2 := ts.after(); err == nil {
		err = err2
	}
	return err
}

// TermOutput is an interface that captures the methods we need from termenv.Output
type TermOutput interface {
	io.Writer
	String(...string) termenv.Style
	ColorProfile() termenv.Profile
}

type promptDone struct{}

func (fe *frontendPretty) handlePromptBool(ctx context.Context, title, message string, dest *bool) error {
	done := make(chan struct{})

	fe.tui.Dispatch(func() {
		fe.handlePromptForm(
			NewForm(
				huh.NewGroup(
					huh.NewConfirm().
						Title(title).
						Description(strings.TrimSpace((&Markdown{
							Content: message,
							Width:   fe.window.Width,
						}).View())).
						Value(dest),
				),
			),
			func(f *huh.Form) { close(done) },
		)
		fe.Compo.Update()
	})

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

func (fe *frontendPretty) handlePromptString(ctx context.Context, title, message string, dest *string) error {
	done := make(chan struct{})

	fe.tui.Dispatch(func() {
		fe.handlePromptForm(
			NewForm(
				huh.NewGroup(
					huh.NewInput().
						Title(title).
						Description(strings.TrimSpace((&Markdown{
							Content: message,
							Width:   fe.window.Width,
						}).View())).
						Value(dest),
				),
			),
			func(f *huh.Form) { close(done) },
		)
		fe.Compo.Update()
	})

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

func handleTelemetryErrorOutput(w io.Writer, to *termenv.Output, err error) {
	if err != nil {
		fmt.Fprintf(w, "%s - %s\n(%s)\n", to.String("WARN").Foreground(termenv.ANSIYellow), "failures detected while emitting telemetry. trace information incomplete", err.Error())
		fmt.Fprintln(w)
	}
}

var (
	ANSIBlack         = lipgloss.Color("0")
	ANSIRed           = lipgloss.Color("1")
	ANSIGreen         = lipgloss.Color("2")
	ANSIYellow        = lipgloss.Color("3")
	ANSIBlue          = lipgloss.Color("4")
	ANSIMagenta       = lipgloss.Color("5")
	ANSICyan          = lipgloss.Color("6")
	ANSIWhite         = lipgloss.Color("7")
	ANSIBrightBlack   = lipgloss.Color("8")
	ANSIBrightRed     = lipgloss.Color("9")
	ANSIBrightGreen   = lipgloss.Color("10")
	ANSIBrightYellow  = lipgloss.Color("11")
	ANSIBrightBlue    = lipgloss.Color("12")
	ANSIBrightMagenta = lipgloss.Color("13")
	ANSIBrightCyan    = lipgloss.Color("14")
	ANSIBrightWhite   = lipgloss.Color("15")
)

type eofMsg struct{}
