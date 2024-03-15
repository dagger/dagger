package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/pprof"
	runtimetrace "runtime/trace"
	"strings"
	"unicode"

	"log/slog"

	"github.com/dagger/dagger/analytics"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/telemetry"
	"github.com/dagger/dagger/telemetry/sdklog"
	"github.com/dagger/dagger/tracing"
	"github.com/muesli/reflow/indent"
	"github.com/muesli/reflow/wordwrap"
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
)

func init() {
	// Disable logrus output, which only comes from the docker
	// commandconn library that is used by buildkit's connhelper
	// and prints unneeded warning logs.
	logrus.StandardLogger().SetOutput(io.Discard)

	rootCmd.PersistentFlags().StringVar(&workdir, "workdir", ".", "The host workdir loaded into dagger")

	rootCmd.PersistentFlags().CountVarP(&verbosity, "verbose", "v", "increase verbosity (use -vv or -vvv for more)")
	rootCmd.PersistentFlags().BoolVarP(&debug, "debug", "d", false, "show debug logs and full verbosity")

	for _, fl := range []string{"workdir"} {
		if err := rootCmd.PersistentFlags().MarkHidden(fl); err != nil {
			fmt.Println("Error hiding flag: "+fl, err)
			os.Exit(1)
		}
	}

	rootCmd.AddCommand(
		listenCmd,
		versionCmd,
		queryCmd,
		runCmd,
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
	rootCmd.SetUsageTemplate(usageTemplate)

	// hide the help flag as it's ubiquitous and thus noisy
	// we'll add it in the last line of the usage template
	rootCmd.PersistentFlags().BoolP("help", "h", false, "Print usage")
	rootCmd.PersistentFlags().Lookup("help").Hidden = true
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

			pprof.StartCPUProfile(profF)
			cobra.OnFinalize(pprof.StopCPUProfile)

			tracePath := cpuprofile + ".trace"

			traceF, err := os.Create(tracePath)
			if err != nil {
				return fmt.Errorf("create trace: %w", err)
			}

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

var Frontend = idtui.New()

func parseGlobalFlags() []string {
	filtered := []string{}
	for _, arg := range os.Args[1:] {
		switch arg {
		case "-v":
			verbosity = 1
		case "-vv":
			verbosity = 2
		case "-vvv":
			verbosity = 3
		case "--debug":
			debug = true
		case "--progress=plain":
			progress = "plain"
		case "--silent":
			silent = true
		default:
			filtered = append(filtered, arg)
		}
	}
	return filtered
}

func Tracer() trace.Tracer {
	return otel.Tracer("dagger.io/cli")
}

func main() {
	rootCmd.SetArgs(parseGlobalFlags())

	Frontend.Debug = debug
	Frontend.Plain = progress == "plain"
	Frontend.Silent = silent
	Frontend.Verbosity = verbosity

	ctx := context.Background()

	if err := Frontend.Run(ctx, func(ctx context.Context) error {
		attrs := []attribute.KeyValue{
			semconv.ServiceName("dagger-cli"),
			semconv.ServiceVersion(engine.Version),
			semconv.ProcessCommandArgs(os.Args...),
		}

		for k, v := range telemetry.LoadDefaultLabels(workdir, engine.Version) {
			attrs = append(attrs, attribute.String(k, v))
		}

		// Init tracing as early as possible and shutdown after the command
		// completes, ensuring progress is fully flushed to the frontend.
		ctx = tracing.Init(ctx, tracing.Config{
			Detect:             true,
			Resource:           resource.NewWithAttributes(semconv.SchemaURL, attrs...),
			LiveTraceExporters: []sdktrace.SpanExporter{Frontend},
			LiveLogExporters:   []sdklog.LogExporter{Frontend},
		})
		defer tracing.Close()

		parentCtx := trace.SpanContextFromContext(ctx)

		// Set the full command string as the name of the root span.
		//
		// If you pass credentials in plaintext, yes, they will be leaked; don't do
		// that, since they will also be leaked in various other places (like the
		// process tree). Use Secret arguments instead.
		ctx, span := Tracer().Start(ctx, strings.Join(os.Args, " "),
			trace.WithAttributes(attribute.Bool(tracing.UIPrimaryAttr, true)))
		defer span.End()

		slog.Debug("established root span",
			"parent", parentCtx.SpanID(),
			"span", span.SpanContext().SpanID(),
			"trace", span.SpanContext().TraceID(),
		)

		ctx, stdout, stderr := tracing.WithStdioToOtel(ctx, "dagger")
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

const usageTemplate = `Usage:

{{- if .Runnable}}
  {{.UseLine}}
{{- end}}
{{- if .HasAvailableSubCommands}}
  {{ .CommandPath}}{{ if .HasAvailableFlags}} [flags]{{end}} [command]
{{- end}}

{{- if gt (len .Aliases) 0}}

Aliases:
  {{.NameAndAliases}}

{{- end}}

{{- if isExperimental .}}

EXPERIMENTAL:
  {{.CommandPath}} is currently under development and may change in the future.

{{- end}}

{{- if .HasExample}}

Examples:
{{ .Example }}

{{- end}}

{{- if .HasAvailableLocalFlags}}

Flags:
{{ flagUsagesWrapped .LocalFlags | trimTrailingWhitespaces}}

{{- end}}

{{- if .HasAvailableSubCommands}}{{$cmds := .Commands}}
{{- if eq (len .Groups) 0}}

Available Commands:
{{- range $cmds }}
{{- if (or .IsAvailableCommand (eq .Name "help"))}}
{{cmdShortWrapped .}}
{{- end}}
{{- end}}

{{- else}}
{{- range $group := .Groups}}

{{.Title}}:
{{- range $cmds }}
{{- if (and (eq .GroupID $group.ID) (or .IsAvailableCommand (eq .Name "help")))}}
{{cmdShortWrapped .}}
{{- end}}
{{- end}}{{/* range $cmds */}}
{{- end}}{{/* range $group := .Groups */}}

{{- if not .AllChildCommandsHaveGroup}}

Additional Commands:
{{- range $cmds }}
{{- if (and (eq .GroupID "") (or .IsAvailableCommand (eq .Name "help")))}}
{{cmdShortWrapped .}}
{{- end}}
{{- end}}{{/* range $cmds */}}
{{- end}}{{/* if not .AllChildCommandsHaveGroup */}}
{{- end}}{{/* if eq (len .Groups) 0 */}}
{{- end}}{{/* if .HasAvailableSubCommands */}}

{{- if .HasAvailableInheritedFlags}}

Global Flags:
{{ flagUsagesWrapped .InheritedFlags | trimTrailingWhitespaces}}

{{- end}}

{{- if .HasHelpSubCommands}}

Additional help topics:
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
