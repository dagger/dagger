package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/muesli/termenv"
	"github.com/vito/go-interact/interact"
	"go.opentelemetry.io/otel/codes"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"golang.org/x/term"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/util/cleanups"
)

const (
	tickInterval = 100 * time.Millisecond // Update frequency for TUI and animation
)

// Dots animation frames (fills up progressively)
var dotsFrames = []rune{'⠀', '⡀', '⣀', '⣄', '⣤', '⣦', '⣶', '⣷', '⣿'}

// checksFrontend is a live TUI frontend for displaying check execution
type checksFrontend struct {
	dagui.FrontendOpts
	mu sync.Mutex

	db      *ChecksDB
	writer  io.Writer
	profile termenv.Profile

	// Check state tracking
	checks         map[string]*checkState // key is check name
	checkOrder     []string               // preserves check display order
	animationFrame int                    // current frame of dots animation
	lastEventTime  time.Time              // time of last span/log event (for activity detection)

	// TUI state
	program *tea.Program
	done    bool
	err     error

	// Execution control
	run      func(context.Context) (cleanups.CleanupF, error)
	runCtx   context.Context
	cleanup  cleanups.CleanupF
	quitting bool

	// Terminal state
	width  int
	height int
}

// checkState tracks the state of a single check
type checkState struct {
	name      string
	status    checkStatus
	span      *dagui.Span
	checkInfo *CheckSpanInfo
	logs      []LogWithContext
	startTime time.Time
	endTime   time.Time
	passed    *bool
}

type checkStatus int

const (
	checkPending checkStatus = iota
	checkRunning
	checkPassed
	checkFailed
)

func (s checkStatus) Emoji() string {
	switch s {
	case checkPending:
		return "⚪"
	case checkRunning:
		return "⏳"
	case checkPassed:
		return "✅"
	case checkFailed:
		return "🔴"
	default:
		return "⚪"
	}
}

// NewChecksFrontend creates a new live frontend for checks
func NewChecksFrontend(w io.Writer, db *ChecksDB) *checksFrontend {
	return &checksFrontend{
		db:             db,
		writer:         w,
		profile:        termenv.ColorProfile(),
		checks:         make(map[string]*checkState),
		checkOrder:     []string{},
		animationFrame: 0,
	}
}

// Run implements the Frontend interface
func (fe *checksFrontend) Run(ctx context.Context, opts dagui.FrontendOpts, f func(context.Context) (cleanups.CleanupF, error)) error {
	fe.FrontendOpts = opts
	fe.run = f
	fe.runCtx = ctx

	// Check if we have a TTY - if not, fallback to non-live mode
	if !fe.isTTY() {
		// Just run the function and return without TUI
		cleanup, err := f(ctx)
		if cleanup != nil {
			defer cleanup()
		}
		return err
	}

	// Start the TUI with alternate screen (clears on exit)
	fe.program = tea.NewProgram(fe, tea.WithAltScreen())

	if _, err := fe.program.Run(); err != nil {
		return err
	}

	// After TUI exits, pretty-print the final results to stdout
	if fe.err == nil {
		fe.db.PrettyPrint(fe.writer)
	}

	return fe.err
}

// isTTY checks if we have a TTY available
func (fe *checksFrontend) isTTY() bool {
	if f, ok := fe.writer.(*os.File); ok {
		return term.IsTerminal(int(f.Fd()))
	}
	return false
}

// Opts implements the Frontend interface
func (fe *checksFrontend) Opts() *dagui.FrontendOpts {
	return &fe.FrontendOpts
}

// SetVerbosity implements the Frontend interface
func (fe *checksFrontend) SetVerbosity(n int) {
	fe.FrontendOpts.Verbosity = n
}

// SetPrimary implements the Frontend interface
func (fe *checksFrontend) SetPrimary(spanID dagui.SpanID) {
	// Not used for checks frontend
}

// Background implements the Frontend interface
func (fe *checksFrontend) Background(cmd tea.ExecCommand, raw bool) error {
	// Not used for checks frontend
	return nil
}

// RevealAllSpans implements the Frontend interface
func (fe *checksFrontend) RevealAllSpans() {
	// Not used for checks frontend
}

// SetCloudURL implements the Frontend interface
func (fe *checksFrontend) SetCloudURL(ctx context.Context, url string, msg string, logged bool) {
	// Not used for checks frontend
}

// SetClient implements the Frontend interface
func (fe *checksFrontend) SetClient(client *dagger.Client) {
	// Not used for checks frontend
}

// Shell implements the Frontend interface
func (fe *checksFrontend) Shell(ctx context.Context, handler idtui.ShellHandler) {
	// Not used for checks frontend
}

// HandleForm implements the Frontend interface
func (fe *checksFrontend) HandleForm(ctx context.Context, form *huh.Form) error {
	// Simple form handling - just run it directly
	return form.RunWithContext(ctx)
}

// HandlePrompt implements the Frontend interface
func (fe *checksFrontend) HandlePrompt(ctx context.Context, title, prompt string, dest any) error {
	// Simple prompt handling using interact
	return interact.NewInteraction(prompt).Resolve(dest)
}

// SetSidebarContent implements the Frontend interface
func (fe *checksFrontend) SetSidebarContent(section idtui.SidebarSection) {
	// Not used for checks frontend - no sidebar in this simple TUI
}

// SpanExporter implements the Frontend interface
// Note: Telemetry flows through ChecksDB, not directly to this frontend
func (fe *checksFrontend) SpanExporter() sdktrace.SpanExporter {
	return fe.db.SpanExporter()
}

// LogExporter implements the Frontend interface
// Note: Telemetry flows through ChecksDB, not directly to this frontend
func (fe *checksFrontend) LogExporter() sdklog.Exporter {
	return fe.db.LogExporter()
}

// MetricExporter implements the Frontend interface
// Note: Telemetry flows through ChecksDB, not directly to this frontend
func (fe *checksFrontend) MetricExporter() sdkmetric.Exporter {
	return fe.db.MetricExporter()
}

// Bubble Tea model implementation

// Init implements tea.Model
func (fe *checksFrontend) Init() tea.Cmd {
	return tea.Batch(
		fe.tick(),
		fe.runChecks(),
	)
}

// Update implements tea.Model
func (fe *checksFrontend) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			fe.quitting = true
			return fe, tea.Quit
		}

	case tea.WindowSizeMsg:
		fe.mu.Lock()
		fe.width = msg.Width
		fe.height = msg.Height
		fe.mu.Unlock()

	case tickMsg:
		fe.mu.Lock()
		fe.animationFrame++
		fe.mu.Unlock()
		return fe, fe.tick()

	case checksCompleteMsg:
		fe.mu.Lock()
		fe.done = true
		fe.err = msg.err
		fe.cleanup = msg.cleanup
		fe.mu.Unlock()
		// Wait a moment so user can see final state
		return fe, tea.Sequence(
			tea.Tick(time.Second, func(time.Time) tea.Msg {
				return quitMsg{}
			}),
		)

	case quitMsg:
		return fe, tea.Quit
	}

	return fe, nil
}

// View implements tea.Model
func (fe *checksFrontend) View() string {
	fe.mu.Lock()
	defer fe.mu.Unlock()

	var b strings.Builder
	now := time.Now()
	if len(fe.checkOrder) == 0 {
		// Show animated progress bar while waiting for checks
		// Only animate if there's been recent activity (within last 500ms)
		hasRecentActivity := !fe.lastEventTime.IsZero() && now.Sub(fe.lastEventTime) < 500*time.Millisecond

		var progressChar rune
		if hasRecentActivity {
			// Animate through dots frames
			progressChar = dotsFrames[fe.animationFrame%len(dotsFrames)]
		} else {
			// Static first frame when no activity
			progressChar = dotsFrames[0]
		}

		b.WriteString(string(progressChar))
		b.WriteString(" ")
		b.WriteString(termenv.String("Loading checks...").Faint().String())
		b.WriteString("\n")
		return b.String()
	}

	// Calculate max log lines per check based on terminal height
	maxLogLines := fe.calculateMaxLogLines()

	// Render each check
	for _, checkName := range fe.checkOrder {
		check, ok := fe.checks[checkName]
		if !ok {
			continue
		}

		fe.renderCheck(&b, check, now, maxLogLines)
		b.WriteString("\n")
	}

	// Footer
	if !fe.done {
		b.WriteString(termenv.String("\nPress 'q' or Ctrl+C to quit").Faint().String())
	} else if fe.err != nil {
		b.WriteString(termenv.String(fmt.Sprintf("\nCompleted with error: %v", fe.err)).Foreground(fe.profile.Color("1")).String())
	} else {
		b.WriteString(termenv.String("\nAll checks completed! Press 'q' or Ctrl+C to exit").Foreground(fe.profile.Color("2")).String())
	}

	return b.String()
}

// calculateMaxLogLines calculates how many log lines to show per check based on terminal height
func (fe *checksFrontend) calculateMaxLogLines() int {
	const (
		minLogLines = 1 // Minimum lines per check when space is tight
		maxLogLines = 3 // Maximum lines per check (keep it compact during visualization)
		headerLines = 2 // Title + blank
		footerLines = 2 // Footer message
	)

	// If height is not set or too small, use minimum
	if fe.height < 10 {
		return minLogLines
	}

	// Count how many checks have logs
	checksWithLogs := 0
	for _, check := range fe.checks {
		if len(check.logs) > 0 {
			checksWithLogs++
		}
	}

	if checksWithLogs == 0 {
		return maxLogLines
	}

	// Calculate available space
	numChecks := len(fe.checkOrder)
	checkStatusLines := numChecks * 2 // 1 line for status + 1 blank line after each check
	usedLines := headerLines + checkStatusLines + footerLines
	availableLines := fe.height - usedLines

	if availableLines < minLogLines {
		return minLogLines
	}

	// Distribute available space among checks with logs
	linesPerCheck := availableLines / checksWithLogs
	if linesPerCheck < minLogLines {
		return minLogLines
	}
	if linesPerCheck > maxLogLines {
		return maxLogLines
	}

	return linesPerCheck
}

// renderCheck renders a single check with its status and details
func (fe *checksFrontend) renderCheck(b *strings.Builder, check *checkState, _ time.Time, maxLogLines int) {
	// Status indicator: animated dots for pending/running, emoji for completed
	if check.status == checkRunning || check.status == checkPending {
		// Animated dots fill-up progress for active checks
		frame := dotsFrames[fe.animationFrame%len(dotsFrames)]
		b.WriteString(string(frame))
	} else {
		// Static emoji for passed/failed
		b.WriteString(check.status.Emoji())
	}
	b.WriteString(" ")

	// Check name with color based on status
	nameColor := fe.profile.Color("") // default
	switch check.status {
	case checkPassed:
		nameColor = fe.profile.Color("2") // green
	case checkFailed:
		nameColor = fe.profile.Color("1") // red
	case checkRunning:
		nameColor = fe.profile.Color("3") // yellow
	}
	b.WriteString(termenv.String(check.name).Bold().Foreground(nameColor).String())

	// Duration (if completed)
	if !check.endTime.IsZero() {
		duration := check.endTime.Sub(check.startTime).Round(time.Millisecond)
		b.WriteString(termenv.String(fmt.Sprintf(" (%v)", duration)).Faint().String())
	}

	b.WriteString("\n")

	// Show streaming logs for all checks
	fe.renderCheckLogs(b, check, maxLogLines)
}

// renderCheckLogs renders streaming logs for a check (last N lines based on terminal height)
func (fe *checksFrontend) renderCheckLogs(b *strings.Builder, check *checkState, maxLogLines int) {
	if len(check.logs) == 0 {
		return // Don't show anything if no logs yet
	}

	// Show last N lines based on terminal height
	logs := check.logs
	if len(logs) > maxLogLines {
		logs = logs[len(logs)-maxLogLines:]
		b.WriteString("  ")
		b.WriteString(termenv.String(fmt.Sprintf("... (showing last %d lines)", maxLogLines)).Faint().String())
		b.WriteString("\n")
	}

	for _, logWithContext := range logs {
		// Build context prefix
		contextPrefix := ""
		if logWithContext.Context != "" {
			contextPrefix = fmt.Sprintf("[%s] ", logWithContext.Context)
		}

		message := logWithContext.Record.Body().AsString()
		lines := strings.Split(message, "\n")

		for _, line := range lines {
			if line == "" {
				continue
			}
			b.WriteString("  ")
			b.WriteString(termenv.String(contextPrefix).Foreground(fe.profile.Color("6")).String())
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
}

// tick returns a command that sends a tick message after the tick interval
func (fe *checksFrontend) tick() tea.Cmd {
	return tea.Tick(tickInterval, func(t time.Time) tea.Msg {
		return tickMsg{time: t}
	})
}

// runChecks starts the check execution in a goroutine
func (fe *checksFrontend) runChecks() tea.Cmd {
	return func() tea.Msg {
		cleanup, err := fe.run(fe.runCtx)
		return checksCompleteMsg{
			cleanup: cleanup,
			err:     err,
		}
	}
}

// onSpanEvent is called when a span is created or updated
func (fe *checksFrontend) onSpanEvent(span *dagui.Span) {
	fe.mu.Lock()
	defer fe.mu.Unlock()

	// Record activity time for progress animation
	fe.lastEventTime = time.Now()

	// Check if this is a check span
	checkInfo := fe.db.extractCheckInfo(span)
	if checkInfo == nil {
		return
	}

	// Get or create check state
	check, ok := fe.checks[checkInfo.Name]
	if !ok {
		check = &checkState{
			name:      checkInfo.Name,
			status:    checkPending,
			startTime: span.StartTime,
		}
		fe.checks[checkInfo.Name] = check
		fe.checkOrder = append(fe.checkOrder, checkInfo.Name)
	}

	// Update check state
	check.span = span
	check.checkInfo = checkInfo

	// Update status based on span state
	if span.EndTime.IsZero() {
		// Span is still running - always show as running
		check.status = checkRunning
	} else {
		// Span has ended - determine final status
		check.endTime = span.EndTime
		if checkInfo.Passed != nil {
			if *checkInfo.Passed {
				check.status = checkPassed
				check.passed = checkInfo.Passed
			} else {
				check.status = checkFailed
				check.passed = checkInfo.Passed
			}
		} else if span.Status.Code == codes.Error {
			check.status = checkFailed
			passed := false
			check.passed = &passed
		} else {
			check.status = checkPassed
			passed := true
			check.passed = &passed
		}
	}

	// Note: Logs are streamed in real-time via onLogEvent, not batch-updated here
}

// onLogEvent is called when a log is emitted
// span parameter is provided to avoid unsafe map access
func (fe *checksFrontend) onLogEvent(logRecord sdklog.Record, span *dagui.Span) {
	fe.mu.Lock()
	defer fe.mu.Unlock()

	// Record activity time for progress animation
	fe.lastEventTime = time.Now()

	// If span is nil (not in map yet), nothing to do
	if span == nil {
		return
	}

	for _, check := range fe.checks {
		if check.span != nil && fe.isDescendantOfSpan(span, check.span) {
			// Add log to check (streaming)
			context := fe.db.extractSpanContext(span)
			logWithContext := LogWithContext{
				Record:  logRecord,
				Context: context,
			}
			check.logs = append(check.logs, logWithContext)

			// Keep a reasonable buffer (more than max display to handle resizing)
			const maxBufferedLogs = 50
			if len(check.logs) > maxBufferedLogs {
				check.logs = check.logs[len(check.logs)-maxBufferedLogs:]
			}
			break
		}
	}
}

// isDescendantOfSpan checks if a span is a descendant of an ancestor span
// Takes span pointers directly to avoid unsafe map access
func (fe *checksFrontend) isDescendantOfSpan(child *dagui.Span, ancestor *dagui.Span) bool {
	if child == nil || ancestor == nil {
		return false
	}

	if child.ID == ancestor.ID {
		return true
	}

	current := child
	for current.ParentSpan != nil {
		current = current.ParentSpan
		if current.ID == ancestor.ID {
			return true
		}
	}

	return false
}

// Bubble Tea messages

type tickMsg struct {
	time time.Time
}

type checksCompleteMsg struct {
	cleanup cleanups.CleanupF
	err     error
}

type quitMsg struct{}
