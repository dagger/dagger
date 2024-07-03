package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/pprof"
	runtimetrace "runtime/trace"
	"sort"
	"strings"
	"unicode"

	"dagger.io/dagger/telemetry"
	"github.com/mattn/go-isatty"
	"github.com/muesli/reflow/indent"
	"github.com/muesli/reflow/wordwrap"
	"github.com/muesli/termenv"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/term"

	"github.com/dagger/dagger/analytics"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/slog"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

var (
	cpuprofile = os.Getenv("CPUPROFILE")
	pprofAddr  = os.Getenv("PPROF")

	execGroup = &cobra.Group{
		ID:    "exec",
		Title: "Execution Commands",
	}

	workdir string

	debug     bool
	verbosity int = idtui.DefaultVerbosity
	silent    bool
	progress  string

	stdoutIsTTY = isatty.IsTerminal(os.Stdout.Fd())
	stderrIsTTY = isatty.IsTerminal(os.Stderr.Fd())

	hasTTY = stdoutIsTTY || stderrIsTTY

	Frontend idtui.Frontend
)

func init() {
	// Disable logrus output, which only comes from the docker
	// commandconn library that is used by buildkit's connhelper
	// and prints unneeded warning logs.
	logrus.StandardLogger().SetOutput(io.Discard)

	rootCmd.AddCommand(
		listenCmd,
		versionCmd,
		queryCmd,
		runCmd,
		watchCmd,
		configCmd,
		moduleInitCmd,
		moduleInstallCmd,
		moduleDevelopCmd,
		modulePublishCmd,
		sessionCmd(),
		newGenCmd(),
	)

	funcCmds.AddParent(rootCmd)

	rootCmd.AddGroup(moduleGroup)
	rootCmd.AddGroup(execGroup)

	cobra.AddTemplateFunc("isExperimental", isExperimental)
	cobra.AddTemplateFunc("flagUsagesWrapped", flagUsagesWrapped)
	cobra.AddTemplateFunc("hasInheritedFlags", hasInheritedFlags)
	cobra.AddTemplateFunc("cmdShortWrapped", cmdShortWrapped)
	cobra.AddTemplateFunc("toUpperBold", toUpperBold)
	cobra.AddTemplateFunc("sortRequiredFlags", sortRequiredFlags)
	cobra.AddTemplateFunc("groupFlags", groupFlags)
	cobra.AddTemplateFunc("indent", indent.String)
	rootCmd.SetUsageTemplate(usageTemplate)

	// hide the help flag as it's ubiquitous and thus noisy
	// we'll add it in the last line of the usage template
	rootCmd.PersistentFlags().BoolP("help", "h", false, "Print usage")
	rootCmd.PersistentFlags().Lookup("help").Hidden = true

	disableFlagsInUseLine(rootCmd)
}

var rootCmd = &cobra.Command{
	Use:           "dagger",
	Short:         "The Dagger CLI provides a command-line interface to Dagger.",
	SilenceErrors: true, // handled in func main() instead
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// if we got this far, CLI parsing worked just fine; no
		// need to show usage for runtime errors
		cmd.SilenceUsage = true

		if cpuprofile != "" {
			profF, err := os.Create(cpuprofile)
			if err != nil {
				return fmt.Errorf("create profile: %w", err)
			}

			pprof.StartCPUProfile(profF)

			tracePath := cpuprofile + ".trace"

			traceF, err := os.Create(tracePath)
			if err != nil {
				return fmt.Errorf("create trace: %w", err)
			}
			if err := runtimetrace.Start(traceF); err != nil {
				return fmt.Errorf("start trace: %w", err)
			}

			cobra.OnFinalize(func() {
				pprof.StopCPUProfile()
				profF.Close()
				runtimetrace.Stop()
				traceF.Close()
			})
		}

		if pprofAddr != "" {
			if err := setupDebugHandlers(pprofAddr); err != nil {
				return fmt.Errorf("start pprof: %w", err)
			}
		}
		var err error
		workdir, err = NormalizeWorkdir(workdir)
		if err != nil {
			return err
		}
		if err := os.Chdir(workdir); err != nil {
			return err
		}

		labels := enginetel.LoadDefaultLabels(workdir, engine.Version)
		t := analytics.New(analytics.DefaultConfig(labels))
		cmd.SetContext(analytics.WithContext(cmd.Context(), t))
		cobra.OnFinalize(func() {
			t.Close()
		})

		if cmdName := commandName(cmd); cmdName != "session" {
			t.Capture(cmd.Context(), "cli_command", map[string]string{
				"name": cmdName,
			})
		}

		return nil
	},
}

func installGlobalFlags(flags *pflag.FlagSet) {
	flags.StringVar(&workdir, "workdir", ".", "The host workdir loaded into dagger")
	flags.CountVarP(&verbosity, "verbose", "v", "increase verbosity (use -vv or -vvv for more)")
	flags.BoolVarP(&debug, "debug", "d", debug, "show debug logs and full verbosity")
	flags.BoolVarP(&silent, "silent", "s", silent, "disable terminal UI and progress output")
	flags.StringVar(&progress, "progress", "auto", "progress output format (auto, plain, tty)")

	for _, fl := range []string{"workdir"} {
		if err := flags.MarkHidden(fl); err != nil {
			fmt.Println("Error hiding flag: "+fl, err)
			os.Exit(1)
		}
	}
}

// disableFlagsInUseLine disables the automatic addition of [flags]
// when calling UseLine.
func disableFlagsInUseLine(cmd *cobra.Command) {
	for _, c := range cmd.Commands() {
		c.DisableFlagsInUseLine = true
		disableFlagsInUseLine(c)
	}
}

func parseGlobalFlags() {
	flags := pflag.NewFlagSet("global", pflag.ContinueOnError)
	flags.Usage = func() {}
	flags.ParseErrorsWhitelist.UnknownFlags = true
	installGlobalFlags(flags)
	if err := flags.Parse(os.Args[1:]); err != nil && !errors.Is(err, pflag.ErrHelp) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func Tracer() trace.Tracer {
	return otel.Tracer("dagger.io/cli")
}

func Resource() *resource.Resource {
	attrs := []attribute.KeyValue{
		semconv.ServiceName("dagger-cli"),
		semconv.ServiceVersion(engine.Version),
		semconv.ProcessCommandArgs(os.Args...),
	}
	for k, v := range enginetel.LoadDefaultLabels(workdir, engine.Version) {
		attrs = append(attrs, attribute.String(k, v))
	}
	return resource.NewWithAttributes(semconv.SchemaURL, attrs...)
}

// ExitError is an error that indicates a command should exit with a specific
// status code, without printing an error message, assuming a human readable
// message has been printed already.
//
// It is basically a shortcut for `os.Exit` while giving the TUI a chance to
// exit gracefully and flush output.
type ExitError struct {
	Code int
}

var Fail = ExitError{Code: 1}

func (e ExitError) Error() string {
	// Not actually printed anywhere.
	return fmt.Sprintf("exit code %d", e.Code)
}

const InstrumentationLibrary = "dagger.io/cli"

func main() {
	parseGlobalFlags()

	opts := idtui.FrontendOpts{
		Debug:  debug,
		Silent: silent,

		// NOTE: the verbosity flag is actually a delta to apply to the
		// internal default verbosity level
		Verbosity: idtui.DefaultVerbosity + verbosity,
	}

	if progress == "auto" {
		if hasTTY {
			progress = "tty"
		} else {
			progress = "plain"
		}
	}
	switch progress {
	case "plain":
		Frontend = idtui.NewPlain()
	case "tty":
		if !hasTTY {
			fmt.Fprintf(os.Stderr, "no tty available for progress %q\n", progress)
			os.Exit(1)
		}
		Frontend = idtui.New()
	default:
		fmt.Fprintf(os.Stderr, "unknown progress type %q\n", progress)
		os.Exit(1)
	}

	installGlobalFlags(rootCmd.PersistentFlags())

	ctx := context.Background()
	ctx = slog.ContextWithColorMode(ctx, termenv.EnvNoColor())
	ctx = slog.ContextWithDebugMode(ctx, debug)

	if err := Frontend.Run(ctx, opts, func(ctx context.Context) (rerr error) {
		ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
		defer stop()

		telemetryCfg := telemetry.Config{
			Detect:   true,
			Resource: Resource(),

			LiveTraceExporters: []sdktrace.SpanExporter{Frontend.SpanExporter()},
			LiveLogExporters:   []sdklog.Exporter{Frontend.LogExporter()},
		}
		if spans, logs, ok := enginetel.ConfiguredCloudExporters(ctx); ok {
			telemetryCfg.LiveTraceExporters = append(telemetryCfg.LiveTraceExporters, spans)
			telemetryCfg.LiveLogExporters = append(telemetryCfg.LiveLogExporters, logs)
		}
		// Init tracing as early as possible and shutdown after the command
		// completes, ensuring progress is fully flushed to the frontend.
		ctx = telemetry.Init(ctx, telemetryCfg)
		defer telemetry.Close()

		// Set the full command string as the name of the root span.
		//
		// If you pass credentials in plaintext, yes, they will be leaked; don't do
		// that, since they will also be leaked in various other places (like the
		// process tree). Use Secret arguments instead.
		ctx, span := Tracer().Start(ctx, strings.Join(os.Args, " "))
		defer telemetry.End(span, func() error { return rerr })

		// Set up global slog to log to the primary span output.
		slog.SetDefault(slog.SpanLogger(ctx, InstrumentationLibrary))

		// Set the span as the primary span for the frontend.
		Frontend.SetPrimary(span.SpanContext().SpanID())

		// Direct command stdout/stderr to span stdio via OpenTelemetry.
		stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)
		defer stdio.Close()
		rootCmd.SetOut(stdio.Stdout)
		rootCmd.SetErr(stdio.Stderr)

		return rootCmd.ExecuteContext(ctx)
	}); err != nil {
		var exit ExitError
		if errors.As(err, &exit) {
			os.Exit(exit.Code)
		} else if errors.Is(err, context.Canceled) {
			os.Exit(2)
		} else {
			fmt.Fprintln(os.Stderr, rootCmd.ErrPrefix(), err)
			os.Exit(1)
		}
	}
}

func NormalizeWorkdir(workdir string) (string, error) {
	if workdir == "" {
		workdir = os.Getenv("DAGGER_WORKDIR")
	}

	if workdir == "" {
		var err error
		workdir, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	workdir, err := filepath.Abs(workdir)
	if err != nil {
		return "", err
	}

	return workdir, nil
}

func commandName(cmd *cobra.Command) string {
	name := []string{}
	for c := cmd; c.Parent() != nil; c = c.Parent() {
		name = append([]string{c.Name()}, name...)
	}
	return strings.Join(name, " ")
}

func isExperimental(cmd *cobra.Command) bool {
	if _, ok := cmd.Annotations["experimental"]; ok {
		return true
	}
	var experimental bool
	cmd.VisitParents(func(cmd *cobra.Command) {
		if _, ok := cmd.Annotations["experimental"]; ok {
			experimental = true
			return
		}
	})
	return experimental
}

func hasInheritedFlags(cmd *cobra.Command) bool {
	if val, ok := cmd.Annotations["help:hideInherited"]; ok && val == "true" {
		return false
	}
	return cmd.HasAvailableInheritedFlags()
}

// getViewWidth returns the width of the terminal, or 80 if it cannot be determined.
func getViewWidth() int {
	width, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		width = 80
	}
	return width - 1
}

// flagUsagesWrapped returns the usage string for all flags in the given FlagSet
// wrapped to the width of the terminal.
func flagUsagesWrapped(flags *pflag.FlagSet) string {
	return flags.FlagUsagesWrapped(getViewWidth())
}

// cmdShortWrapped returns the short description for the given command wrapped
// to the width of the terminal.
//
// This reduces visual noise by preventing `c.Short` descriptions from showing
// above the next command name.
//
// Ideally `c.Short` fields should be as short as possible.
func cmdShortWrapped(c *cobra.Command) string {
	width := getViewWidth()

	// Produce the same string length for all sibling commands by padding to
	// the right based on the longest name. Add two extra spaces to the left
	// of the screen, and three extra spaces before the description.
	nameFormat := fmt.Sprintf("  %%-%ds   ", c.NamePadding())
	name := fmt.Sprintf(nameFormat, c.Name())

	description := c.Short
	if len(name)+len(description) >= width {
		wrapped := wordwrap.String(c.Short, width-len(name))
		indented := indent.String(wrapped, uint(len(name)))
		// first line shouldn't be indented since we're going to prepend the name
		description = strings.TrimLeftFunc(indented, unicode.IsSpace)
	}

	return name + description
}

// toUpperBold returns the given string in uppercase and bold.
func toUpperBold(s string) string {
	upperCase := strings.ToUpper(s)
	return termenv.String(upperCase).Bold().String()
}

// sortRequiredFlags separates optional flags from required flags.
func sortRequiredFlags(originalFlags *pflag.FlagSet) *pflag.FlagSet {
	mergedFlags := pflag.NewFlagSet("merged", pflag.ContinueOnError)
	mergedFlags.SortFlags = false

	optionalFlags := pflag.NewFlagSet("optional", pflag.ContinueOnError)

	// separate optional flags from required flags
	originalFlags.VisitAll(func(flag *pflag.Flag) {
		// Append [required] and show required flags first
		requiredAnnotation, found := flag.Annotations[cobra.BashCompOneRequiredFlag]
		if found && requiredAnnotation[0] == "true" {
			flag.Usage = strings.TrimSpace(flag.Usage + " [required]")
			mergedFlags.AddFlag(flag)
		} else {
			optionalFlags.AddFlag(flag)
		}
	})

	// Add optional flags back, after all required flags
	mergedFlags.AddFlagSet(optionalFlags)

	return mergedFlags
}

type FlagGroup struct {
	Title string
	Flags *pflag.FlagSet
}

func groupFlags(flags *pflag.FlagSet) string {
	grouped := make(map[string]*pflag.FlagSet)
	defaultGroup := "Options"

	flags.VisitAll(func(flag *pflag.Flag) {
		group := defaultGroup
		value, found := flag.Annotations["help:group"]
		if found {
			group = strings.Join(value, " ")
		}
		if _, ok := grouped[group]; !ok {
			grouped[group] = pflag.NewFlagSet(group, pflag.ContinueOnError)
		}
		grouped[group].AddFlag(flag)
	})

	groups := make([]FlagGroup, 0, len(grouped))
	for k, v := range grouped {
		groups = append(groups, FlagGroup{k, v})
	}

	sort.Slice(groups, func(i, j int) bool {
		return groups[i].Title < groups[j].Title
	})

	var builder strings.Builder
	for _, group := range groups {
		builder.WriteString(toUpperBold(group.Title))
		builder.WriteString("\n")
		builder.WriteString(flagUsagesWrapped(sortRequiredFlags(group.Flags)))
		builder.WriteString("\n")
	}

	return builder.String()
}

const usageTemplate = `{{ if .Runnable}}{{ "Usage" | toUpperBold }}
  {{.UseLine}}{{ end }}

{{- if gt (len .Aliases) 0}}

{{ "Aliases" | toUpperBold }}
  {{.NameAndAliases}}

{{- end}}

{{- if isExperimental .}}

{{ "EXPERIMENTAL" | toUpperBold }}
  {{.CommandPath}} is currently under development and may change in the future.

{{- end}}

{{- if .HasExample}}

{{ "Examples" | toUpperBold }}
{{ indent .Example 2 }}

{{- end}}

{{- if .HasAvailableSubCommands}}{{$cmds := .Commands}}
{{- if eq (len .Groups) 0}}

{{ "Available Commands" | toUpperBold }}
{{- range $cmds }}
{{- if (or .IsAvailableCommand (eq .Name "help"))}}
{{cmdShortWrapped .}}
{{- end}}
{{- end}}

{{- else}}
{{- range $group := .Groups}}

{{.Title | toUpperBold}}
{{- range $cmds }}
{{- if (and (eq .GroupID $group.ID) (or .IsAvailableCommand (eq .Name "help")))}}
{{cmdShortWrapped .}}
{{- end}}
{{- end}}{{/* range $cmds */}}
{{- end}}{{/* range $group := .Groups */}}

{{- if not .AllChildCommandsHaveGroup}}

{{ "Additional Commands" | toUpperBold }}
{{- range $cmds }}
{{- if (and (eq .GroupID "") (or .IsAvailableCommand (eq .Name "help")))}}
{{cmdShortWrapped .}}
{{- end}}
{{- end}}{{/* range $cmds */}}
{{- end}}{{/* if not .AllChildCommandsHaveGroup */}}
{{- end}}{{/* if eq (len .Groups) 0 */}}
{{- end}}{{/* if .HasAvailableSubCommands */}}

{{- if .HasAvailableLocalFlags}}

{{ groupFlags .LocalFlags | trimTrailingWhitespaces }}

{{- end}}

{{- if hasInheritedFlags . }}

{{ "Inherited Options" | toUpperBold }}
{{ flagUsagesWrapped .InheritedFlags | trimTrailingWhitespaces }}

{{- end}}

{{- if .HasHelpSubCommands}}

{{ "Additional help topics" | toUpperBold }}
{{- range .Commands}}
{{- if .IsAdditionalHelpTopicCommand}}
  {{rpad .CommandPath .CommandPathPadding}} {{.Short}}
{{- end}}
{{- end}}

{{- end}}{{/* if .HasHelpSubCommands */}}

{{- if .HasAvailableSubCommands }}

Use "{{.CommandPath}} [command] --help" for more information about a command.
{{- end}}
`
