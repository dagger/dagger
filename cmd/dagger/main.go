package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/pprof"
	runtimetrace "runtime/trace"
	"strings"
	"unicode"

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
	"github.com/dagger/dagger/telemetry"
	"github.com/dagger/dagger/telemetry/sdklog"
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
	verbosity int
	silent    bool
	progress  string

	stdoutIsTTY = isatty.IsTerminal(os.Stdout.Fd())
	stderrIsTTY = isatty.IsTerminal(os.Stderr.Fd())

	autoTTY = stdoutIsTTY || stderrIsTTY

	Frontend = idtui.New()
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
	cobra.AddTemplateFunc("cmdShortWrapped", cmdShortWrapped)
	cobra.AddTemplateFunc("toUpperBold", toUpperBold)
	cobra.AddTemplateFunc("separateAndModifyFlags", separateAndModifyFlags)
	rootCmd.SetUsageTemplate(usageTemplate)

	// hide the help flag as it's ubiquitous and thus noisy
	// we'll add it in the last line of the usage template
	rootCmd.PersistentFlags().BoolP("help", "h", false, "Print usage")
	rootCmd.PersistentFlags().Lookup("help").Hidden = true

	disableFlagsInUseLine(rootCmd)
}

var rootCmd = &cobra.Command{
	Use:   "dagger",
	Short: "The Dagger CLI provides a command-line interface to Dagger.",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// if we got this far, CLI parsing worked just fine; no
		// need to show usage for runtime errors
		cmd.SilenceUsage = true

		if cpuprofile != "" {
			profF, err := os.Create(cpuprofile)
			if err != nil {
				return fmt.Errorf("create profile: %w", err)
			}
			defer profF.Close()
			pprof.StartCPUProfile(profF)
			cobra.OnFinalize(pprof.StopCPUProfile)

			tracePath := cpuprofile + ".trace"

			traceF, err := os.Create(tracePath)
			if err != nil {
				return fmt.Errorf("create trace: %w", err)
			}
			defer traceF.Close()
			if err := runtimetrace.Start(traceF); err != nil {
				return fmt.Errorf("start trace: %w", err)
			}
			cobra.OnFinalize(runtimetrace.Stop)
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

		labels := telemetry.LoadDefaultLabels(workdir, engine.Version)
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
	flags.BoolVarP(&debug, "debug", "d", false, "show debug logs and full verbosity")
	flags.BoolVarP(&silent, "silent", "s", false, "disable terminal UI and progress output")
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
	for k, v := range telemetry.LoadDefaultLabels(workdir, engine.Version) {
		attrs = append(attrs, attribute.String(k, v))
	}
	return resource.NewWithAttributes(semconv.SchemaURL, attrs...)
}

func main() {
	parseGlobalFlags()

	Frontend.Debug = debug
	Frontend.Plain = progress == "plain"
	Frontend.Silent = silent
	Frontend.Verbosity = verbosity

	installGlobalFlags(rootCmd.PersistentFlags())

	ctx := context.Background()

	if err := Frontend.Run(ctx, func(ctx context.Context) (rerr error) {
		// Init tracing as early as possible and shutdown after the command
		// completes, ensuring progress is fully flushed to the frontend.
		ctx = telemetry.Init(ctx, telemetry.Config{
			Detect:             true,
			Resource:           Resource(),
			LiveTraceExporters: []sdktrace.SpanExporter{Frontend},
			LiveLogExporters:   []sdklog.LogExporter{Frontend},
		})
		defer telemetry.Close()

		// Set the full command string as the name of the root span.
		//
		// If you pass credentials in plaintext, yes, they will be leaked; don't do
		// that, since they will also be leaked in various other places (like the
		// process tree). Use Secret arguments instead.
		ctx, span := Tracer().Start(ctx, strings.Join(os.Args, " "))
		defer telemetry.End(span, func() error { return rerr })

		// Set the span as the primary span for the frontend.
		Frontend.SetPrimary(span.SpanContext().SpanID())

		// Direct command stdout/stderr to span logs via OpenTelemetry.
		ctx, stdout, stderr := telemetry.WithStdioToOtel(ctx, "dagger")
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(stderr)

		return rootCmd.ExecuteContext(ctx)
	}); err != nil {
		os.Exit(1)
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

// separateAndModifyFlags separates optional flags from required flags
func separateAndModifyFlags(originalFlags *pflag.FlagSet) *pflag.FlagSet {
	mergedFlags := pflag.NewFlagSet("merged", pflag.ContinueOnError)
	mergedFlags.SortFlags = false

	optionalFlags := pflag.NewFlagSet("optional", pflag.ContinueOnError)

	// separate optional flags from required flags
	originalFlags.VisitAll(func(flag *pflag.Flag) {
		clone := *flag

		// Append [required] and show required flags first
		if flag.Annotations["cobra_annotation_bash_completion_one_required_flag"] != nil {
			clone.Usage += " [required]"
			mergedFlags.AddFlag(&clone)
		} else {
			optionalFlags.AddFlag(&clone)
		}
	})

	// Add optional flags back, after all required flags
	optionalFlags.VisitAll(func(flag *pflag.Flag) {
		mergedFlags.AddFlag(flag)
	})

	return mergedFlags
}

const usageTemplate = `{{ "Usage" | toUpperBold }}

{{- if .Runnable}}
  {{.UseLine}}
{{- end}}
{{- if .HasAvailableSubCommands}}
  {{ .CommandPath}}{{ if .HasAvailableFlags}} [options]{{end}} [command]
{{- end}}

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
{{ .Example }}

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

{{ "Options" | toUpperBold }}
{{ flagUsagesWrapped (separateAndModifyFlags .LocalFlags) | trimTrailingWhitespaces}}

{{- end}}

{{- if .HasAvailableInheritedFlags}}

{{ "Inherited Options" | toUpperBold }}
{{ flagUsagesWrapped .InheritedFlags | trimTrailingWhitespaces}}

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
