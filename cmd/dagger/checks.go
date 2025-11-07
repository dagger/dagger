package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/juju/ansiterm/tabwriter"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"dagger.io/dagger"
	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/slog"
)

var (
	checksListMode          bool
	checksDB                *ChecksDB // Global instance for telemetry collection
	checksUsingLiveFrontend bool      // True when Frontend is swapped to checksFrontend
)

var checksCmd = &cobra.Command{
	Hidden: true,
	Use:    "checks [options] [pattern...]",
	Short:  "Load and execute checks",
	Long: `Load and execute checks.

Checks are a convenience layer over regular Dagger Functions

Examples:
  dagger checks                    # Run all checks
  dagger checks -l                 # List all checks without running
  dagger checks pattern1 pattern2  # Run checks matching patterns
`,
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Initialize checks DB BEFORE withEngine so it's available for telemetry registration
		checksDB = NewChecksDB()
		defer func() {
			checksDB = nil // cleanup
		}()

		// List mode - no visualization
		return withEngine(cmd.Context(), client.Params{}, func(ctx context.Context, engineClient *client.Client) error {
			dag := engineClient.Dagger()
			mod, err := loadModule(ctx, dag)
			if err != nil {
				return err
			}
			var checks *dagger.CheckGroup
			if len(args) > 0 {
				checks = mod.Checks(dagger.ModuleChecksOpts{Include: args})
			} else {
				checks = mod.Checks()
			}
			if checksListMode {
				return listChecks(ctx, checks, cmd)
			} else {
				return runChecks(ctx, checks, cmd)
			}
		})
	},
}

func loadModule(ctx context.Context, dag *dagger.Client) (*dagger.Module, error) {
	modRef, _ := getExplicitModuleSourceRef()
	if modRef == "" {
		modRef = moduleURLDefault
	}
	ctx, span := Tracer().Start(ctx, "load "+modRef)
	defer span.End()
	return dag.ModuleSource(modRef).AsModule().Sync(ctx)
}

func loadCheckGroupInfo(ctx context.Context, checks []dagger.Check) (*CheckGroupInfo, error) {
	ctx, span := Tracer().Start(ctx, "fetch check information")
	defer span.End()

	info := &CheckGroupInfo{}
	for _, check := range checks {
		checkInfo := &CheckInfo{}

		name, err := check.Name(ctx)
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
			return nil, err
		}
		checkInfo.Name = cliName(name)

		description, err := check.Description(ctx)
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
			return nil, err
		}
		checkInfo.Description = description

		info.Checks = append(info.Checks, checkInfo)
	}
	return info, nil
}

type CheckGroupInfo struct {
	Checks []*CheckInfo
}

type CheckInfo struct {
	Name        string
	Description string
}

// 'dagger checks -l'
func listChecks(ctx context.Context, checkgroup *dagger.CheckGroup, cmd *cobra.Command) error {
	checks, err := checkgroup.List(ctx)
	if err != nil {
		return err
	}
	info, err := loadCheckGroupInfo(ctx, checks)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', tabwriter.DiscardEmptyColumns)
	fmt.Fprintf(tw, "%s\t%s\n",
		termenv.String("Name").Bold(),
		termenv.String("Description").Bold(),
	)
	for _, check := range info.Checks {
		firstLine := check.Description
		if idx := strings.Index(check.Description, "\n"); idx != -1 {
			firstLine = check.Description[:idx]
		}
		fmt.Fprintf(tw, "%s\t%s\n", check.Name, firstLine)
	}
	return tw.Flush()
}

// 'dagger checks' (runs by default)
func runChecks(ctx context.Context, checkgroup *dagger.CheckGroup, _ *cobra.Command) error {
	ctx, shellSpan := Tracer().Start(ctx, "checks", telemetry.Passthrough())
	defer telemetry.End(shellSpan, func() error { return nil })
	Frontend.SetPrimary(dagui.SpanID{SpanID: shellSpan.SpanContext().SpanID()})
	slog.SetDefault(slog.SpanLogger(ctx, InstrumentationLibrary))
	// We don't actually use the API for rendering results
	// Instead, we rely on telemetry
	// FIXME: this feels a little weird. Can we move the relevant telemetry collection in the API?
	checks, err := checkgroup.Run().List(ctx)
	if err != nil {
		return err
	}
	var failed int
	for _, check := range checks {
		passed, err := check.Passed(ctx)
		if err != nil {
			return err
		}
		if !passed {
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d checks failed", failed)
	}
	return nil
}

// ChecksDB extends dagui.DB to capture logs for check spans and their children
type ChecksDB struct {
	*dagui.DB
	// Cache of span IDs that are check-related (for performance)
	checkRelatedCache map[dagui.SpanID]bool
	profile           termenv.Profile
	// Optional frontend for live updates
	frontend *checksFrontend
	mu       sync.Mutex
	// Protects concurrent access to spanCache and DB operations
	exportMu  sync.Mutex
	spanCache map[dagui.SpanID]*dagui.Span // Our own thread-safe span cache
}

func NewChecksDB() *ChecksDB {
	return &ChecksDB{
		DB:                dagui.NewDB(),
		checkRelatedCache: make(map[dagui.SpanID]bool),
		profile:           termenv.ColorProfile(),
		spanCache:         make(map[dagui.SpanID]*dagui.Span),
	}
}

// SetFrontend connects a live frontend to this ChecksDB for real-time updates
func (db *ChecksDB) SetFrontend(fe *checksFrontend) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.frontend = fe
}

// notifySpanUpdate notifies the frontend of a span update
func (db *ChecksDB) notifySpanUpdate(span *dagui.Span) {
	db.mu.Lock()
	frontend := db.frontend
	db.mu.Unlock()

	if frontend != nil {
		frontend.onSpanEvent(span)
	}
}

// notifyLogUpdate notifies the frontend of a log event
// Must be called while holding db.exportMu to safely access Spans.Map
func (db *ChecksDB) notifyLogUpdate(logRecord sdklog.Record, span *dagui.Span) {
	db.mu.Lock()
	frontend := db.frontend
	db.mu.Unlock()

	if frontend != nil {
		frontend.onLogEvent(logRecord, span)
	}
}

func (db *ChecksDB) PrettyPrint(w io.Writer) {
	// Find all check spans by searching for the check attribute
	checkSpans := db.findCheckSpans()
	if len(checkSpans) == 0 {
		return // No check spans found
	}

	// Render status line for all checks, logs only for failed checks
	for _, checkInfo := range checkSpans {
		// Always print status line (emoji + name + duration)
		db.renderCheckStatus(w, checkInfo)

		// Determine if check failed
		isFailed := false
		if checkInfo.Passed != nil && !*checkInfo.Passed {
			isFailed = true
		} else if checkInfo.Passed == nil && checkInfo.Span.Status.Code.String() == "ERROR" {
			isFailed = true
		}

		// Only print logs for failed checks
		if isFailed {
			db.renderCheckLogs(w, checkInfo)
		}
	}
}

// renderCheckStatus renders just the status line (emoji + name + duration)
func (db *ChecksDB) renderCheckStatus(w io.Writer, checkInfo *CheckSpanInfo) {
	span := checkInfo.Span

	// Determine status and color
	status := "⚪"
	statusColor := db.profile.Color("8") // gray

	if checkInfo.Passed != nil {
		if *checkInfo.Passed {
			status = "🟢"
			statusColor = db.profile.Color("2") // green
		} else {
			status = "🔴"
			statusColor = db.profile.Color("1") // red
		}
	} else if span.Status.Code == codes.Error {
		status = "🔴"
		statusColor = db.profile.Color("1") // red
	}

	// Calculate duration
	duration := ""
	if !span.EndTime.IsZero() {
		duration = fmt.Sprintf(" (%v)", span.EndTime.Sub(span.StartTime).Round(time.Millisecond))
	}

	// Use the extracted check name, falling back to span name
	displayName := checkInfo.Name
	if displayName == "" {
		displayName = span.Name
	}

	fmt.Fprintf(w, "%s %s%s\n",
		status,
		termenv.String(displayName).Bold().Foreground(statusColor),
		termenv.String(duration).Faint(),
	)
}

// renderCheckLogs renders just the logs for a check (no status line)
func (db *ChecksDB) renderCheckLogs(w io.Writer, checkInfo *CheckSpanInfo) {
	span := checkInfo.Span

	// Get filtered logs for check span and its descendants
	logs := db.getFilteredCheckLogs(span)
	if len(logs) == 0 {
		fmt.Fprintf(w, "  %s\n",
			termenv.String("No logs available").Faint().Italic())
		return
	}

	// Render the logs with context
	for _, logWithContext := range logs {
		db.renderLogRecordWithContext(w, logWithContext)
	}
}

// LogWithContext pairs a log record with its context label
type LogWithContext struct {
	Record  sdklog.Record
	Context string // e.g., "go", "myFunction", etc.
}

// findCheckSpans finds all spans that have the check attribute
func (db *ChecksDB) findCheckSpans() []*CheckSpanInfo {
	var checkSpans []*CheckSpanInfo

	// Iterate through all spans to find check-related ones
	for _, span := range db.Spans.Order {
		if span == nil {
			continue
		}

		// Check if this span has the check attribute
		if checkInfo := db.extractCheckInfo(span); checkInfo != nil {
			checkSpans = append(checkSpans, checkInfo)
		}
	}

	return checkSpans
}

// shouldIncludeLogs determines if a span's logs should be included
func (db *ChecksDB) shouldIncludeLogs(_ *dagui.Span) bool {
	// TODO: Filter out system/module loading spans
	// For now, include all spans
	return true
}

// getFilteredCheckLogs collects logs with smart filtering
func (db *ChecksDB) getFilteredCheckLogs(checkSpan *dagui.Span) []LogWithContext {
	var result []LogWithContext

	// Recursively collect logs from spans
	var collectLogs func(*dagui.Span)
	collectLogs = func(span *dagui.Span) {
		// Check if this span should have its logs included
		if db.shouldIncludeLogs(span) {
			context := db.extractSpanContext(span)
			for _, logRecord := range db.PrimaryLogs[span.ID] {
				result = append(result, LogWithContext{
					Record:  logRecord,
					Context: context,
				})
			}
		}

		// Recurse into children
		for _, child := range span.ChildSpans.Order {
			collectLogs(child)
		}
	}

	collectLogs(checkSpan)
	return result
}

// extractSpanContext extracts a meaningful context label from a span
func (db *ChecksDB) extractSpanContext(span *dagui.Span) string {
	call := span.Call()
	if call == nil {
		return span.Name
	}

	// Handle withExec: extract first argument (the command)
	if call.Field == "withExec" {
		if len(call.Args) > 0 && call.Args[0].Name == "args" {
			// The args value is a list literal
			if argList := call.Args[0].Value.GetList(); argList != nil {
				if len(argList.Values) > 0 {
					// Extract just the command name (first element of the list)
					cmd := argList.Values[0].GetString_()
					if cmd != "" {
						return cmd
					}
				}
			}
		}
		return "exec"
	}

	// For function calls, use the function name
	if call.Field != "" {
		return call.Field
	}

	// Fallback to span name
	return span.Name
}

// renderLogRecordWithContext renders a log record with context prefix
func (db *ChecksDB) renderLogRecordWithContext(w io.Writer, logWithContext LogWithContext) {
	record := logWithContext.Record

	// Determine message color based on log level
	messageColor := db.profile.Color("") // default (no color)
	if record.Severity() >= log.SeverityError {
		messageColor = db.profile.Color("1") // red
	} else if record.Severity() >= log.SeverityWarn {
		messageColor = db.profile.Color("3") // yellow
	}

	// Format the log message
	message := record.Body().AsString()

	// Build context prefix
	contextPrefix := ""
	if logWithContext.Context != "" {
		contextPrefix = fmt.Sprintf("[%s] ", logWithContext.Context)
	}

	// Split message by newlines to handle multi-line log entries
	lines := strings.Split(message, "\n")

	// Render each line with context prefix
	for _, line := range lines {
		fmt.Fprintf(w, "  %s%s\n",
			termenv.String(contextPrefix).Foreground(db.profile.Color("6")), // cyan for context
			termenv.String(line).Foreground(messageColor),
		)
	}

	// Render attributes if any
	record.WalkAttributes(func(kv log.KeyValue) bool {
		if kv.Key != "" && kv.Value.AsString() != "" {
			fmt.Fprintf(w, "    %s=%s\n",
				termenv.String(kv.Key).Faint(),
				kv.Value.AsString(),
			)
		}
		return true
	})
}

// CheckSpanInfo holds information about a check span
type CheckSpanInfo struct {
	Span   *dagui.Span
	Name   string
	Passed *bool
}

// extractCheckInfo extracts check information from span attributes
func (db *ChecksDB) extractCheckInfo(span *dagui.Span) *CheckSpanInfo {
	// Look for the check attribute in ExtraAttributes
	if span.ExtraAttributes == nil {
		return nil
	}
	info := &CheckSpanInfo{
		Span: span,
		Name: span.Name, // Default name
	}
	if checkNameRaw, hasCheckName := span.ExtraAttributes[telemetry.CheckNameAttr]; !hasCheckName {
		return nil
	} else {
		var name string
		if err := json.Unmarshal(checkNameRaw, &name); err == nil {
			info.Name = name
		}
	}

	// Extract check passed status if available
	if checkPassedRaw, hasCheckPassed := span.ExtraAttributes[telemetry.CheckPassedAttr]; hasCheckPassed {
		var passed bool
		if err := json.Unmarshal(checkPassedRaw, &passed); err == nil {
			info.Passed = &passed
		}
	}

	return info
}

// SpanExporter returns a custom exporter that processes spans and notifies frontend
func (db *ChecksDB) SpanExporter() sdktrace.SpanExporter {
	return &ChecksSpanExporter{db: db}
}

// LogExporter returns a custom exporter that buffers logs for check spans
func (db *ChecksDB) LogExporter() sdklog.Exporter {
	return &ChecksLogExporter{db: db}
}

type ChecksSpanExporter struct {
	db *ChecksDB
}

func (cse *ChecksSpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	// Serialize access to DB operations and spanCache
	cse.db.exportMu.Lock()
	defer cse.db.exportMu.Unlock()

	// First, let the underlying DB process the spans
	if err := cse.db.DB.ExportSpans(ctx, spans); err != nil {
		return err
	}

	// Update our spanCache and notify frontend for each span
	for _, roSpan := range spans {
		spanID := dagui.SpanID{SpanID: roSpan.SpanContext().SpanID()}
		if span := cse.db.DB.Spans.Map[spanID]; span != nil {
			// Cache the span pointer for thread-safe access
			cse.db.spanCache[spanID] = span
			cse.db.notifySpanUpdate(span)
		}
	}

	return nil
}

func (cse *ChecksSpanExporter) Shutdown(ctx context.Context) error {
	return nil
}

type ChecksLogExporter struct {
	db *ChecksDB
}

// Export captures logs for check-related spans (check + descendants)
func (cle *ChecksLogExporter) Export(ctx context.Context, logs []sdklog.Record) error {
	// Serialize access to DB maps to prevent concurrent map access
	cle.db.exportMu.Lock()
	defer cle.db.exportMu.Unlock()

	for _, logRecord := range logs {
		if logRecord.Body().AsString() == "" {
			continue
		}

		spanID := dagui.SpanID{SpanID: logRecord.SpanID()}

		// Look up span from our thread-safe cache
		span := cle.db.spanCache[spanID]

		// Store logs for spans that are checks OR descendants of checks
		if cle.isCheckRelated(spanID) {
			cle.db.DB.PrimaryLogs[spanID] = append(cle.db.DB.PrimaryLogs[spanID], logRecord)

			// Notify frontend of log event (pass span to avoid map access without lock)
			cle.db.notifyLogUpdate(logRecord, span)
		}

		// Mark that logs exist (for HasLogs flag) if span already exists
		if span != nil {
			span.HasLogs = true
		}
	}
	return nil
}

// isCheckRelated determines if a span is a check or descendant of a check
func (cle *ChecksLogExporter) isCheckRelated(spanID dagui.SpanID) bool {
	// Check cache first
	if isCheck, cached := cle.db.checkRelatedCache[spanID]; cached {
		return isCheck
	}

	// Use our thread-safe spanCache instead of DB.Spans.Map
	span := cle.db.spanCache[spanID]
	if span == nil {
		// Span not yet received, assume not check-related for now
		// Will be reevaluated when span is exported
		return false
	}

	result := cle.checkRelatedUncached(span)
	cle.db.checkRelatedCache[spanID] = result
	return result
}

// checkRelatedUncached performs the actual check without caching
func (cle *ChecksLogExporter) checkRelatedUncached(span *dagui.Span) bool {
	// Check if this span itself is a check
	if span.ExtraAttributes != nil {
		if _, ok := span.ExtraAttributes["dagger.io/check.hidelogs"]; ok {
			// FIXME: support setting to false. For now we interpret the attribute existence as 'true'
			return false
		}
		if _, hasCheckAttr := span.ExtraAttributes[telemetry.CheckNameAttr]; hasCheckAttr {
			return true
		}
	}
	// Recursively check parents (if any parent is a check, this is a descendant)
	current := span
	for current.ParentSpan != nil {
		current = current.ParentSpan
		if current.ExtraAttributes != nil {
			if _, ok := current.ExtraAttributes["dagger.io/check.hidelogs"]; ok {
				// FIXME: support setting to false. For now we interpret the attribute existence as 'true'
				return false
			}
			if _, hasCheckAttr := current.ExtraAttributes[telemetry.CheckNameAttr]; hasCheckAttr {
				return true
			}
		}
	}
	return false
}

func (cle *ChecksLogExporter) Shutdown(ctx context.Context) error {
	return nil
}

func (cle *ChecksLogExporter) ForceFlush(ctx context.Context) error {
	return nil
}

// runChecksWithLiveVisualization runs checks with live TUI visualization
func runChecksWithLiveVisualization(ctx context.Context, args []string, cmd *cobra.Command) error {
	// Create the live frontend
	frontend := NewChecksFrontend(cmd.OutOrStdout(), checksDB)

	// Check if we have a TTY - if not, fallback to non-live mode
	if !frontend.isTTY() {
		// Fallback to traditional post-execution rendering
		err := withEngine(ctx, client.Params{}, func(ctx context.Context, engineClient *client.Client) error {
			dag := engineClient.Dagger()
			mod, err := loadModule(ctx, dag)
			if err != nil {
				return err
			}
			var checks *dagger.CheckGroup
			if len(args) > 0 {
				checks = mod.Checks(dagger.ModuleChecksOpts{Include: args})
			} else {
				checks = mod.Checks()
			}
			return runChecks(ctx, checks, cmd)
		})
		if err != nil {
			return err
		}
		// Render results after execution
		checksDB.PrettyPrint(os.Stdout)
		return nil
	}

	// Connect frontend to ChecksDB for live updates
	checksDB.SetFrontend(frontend)
	defer func() {
		checksDB.SetFrontend(nil) // Clean up connection
	}()

	// Temporarily replace the global Frontend with our checks frontend
	// This prevents withEngine() from starting a competing TUI
	originalFrontend := Frontend
	Frontend = frontend
	checksUsingLiveFrontend = true
	defer func() {
		Frontend = originalFrontend
		checksUsingLiveFrontend = false
	}()

	// Use live visualization via withEngine (which calls Frontend.Run())
	return withEngine(ctx, client.Params{}, func(ctx context.Context, engineClient *client.Client) error {
		dag := engineClient.Dagger()
		mod, err := loadModule(ctx, dag)
		if err != nil {
			return err
		}
		var checks *dagger.CheckGroup
		if len(args) > 0 {
			checks = mod.Checks(dagger.ModuleChecksOpts{Include: args})
		} else {
			checks = mod.Checks()
		}
		return runChecks(ctx, checks, cmd)
	})
}

func init() {
	checksCmd.Flags().BoolVarP(&checksListMode, "list", "l", false, "List checks without running them")
}
