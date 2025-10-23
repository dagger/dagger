package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"maps"
	"os"
	"os/signal"
	"runtime/pprof"
	runtimetrace "runtime/trace"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/google/shlex"
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
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/term"
	"mvdan.cc/sh/v3/interp"

	"github.com/dagger/dagger/analytics"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client/pathutil"
	"github.com/dagger/dagger/engine/slog"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/internal/cloud/auth"
)

var (
	cpuprofile = os.Getenv("CPUPROFILE")
	pprofAddr  = os.Getenv("PPROF")

	execGroup = &cobra.Group{
		ID:    "exec",
		Title: "Execution Commands",
	}

	workdir string

	silent                   bool
	verbose                  int
	quiet, _                 = strconv.Atoi(os.Getenv("DAGGER_QUIET"))
	reveal                   = os.Getenv("DAGGER_REVEAL") != ""
	debugFlag                bool
	progress                 string
	interactive              bool
	interactiveCommand       string
	interactiveCommandParsed []string
	web                      bool
	noExit                   bool
	_, useCloudEngine        = os.LookupEnv("DAGGER_CLOUD_ENGINE")

	dotOutputFilePath string
	dotFocusField     string
	dotShowInternal   bool

	stdoutIsTTY = isatty.IsTerminal(os.Stdout.Fd())
	stderrIsTTY = isatty.IsTerminal(os.Stderr.Fd())

	hasTTY = stdoutIsTTY || stderrIsTTY

	Frontend idtui.Frontend
)

func init() {
	// allow user explicitly setting progress via env, but default it to "auto"
	// otherwise
	if progress == "" {
		progress = "auto"
	}
}

var (
	stdin          io.Reader
	stdout, stderr io.Writer
)

func logFileFromEnv(envName string) *os.File {
	if logFileName := os.Getenv(envName); logFileName != "" {
		f, err := os.Create(logFileName)
		if err != nil {
			panic(err)
		}
		return f
	}
	return nil
}

func maybeWrapReader(r io.Reader, envName string) io.Reader {
	f := logFileFromEnv(envName)
	if f == nil {
		return r
	}
	return io.TeeReader(r, f)
}

func maybeWrapWriter(w io.Writer, envName string) io.Writer {
	f := logFileFromEnv(envName)
	if f == nil {
		return w
	}
	return io.MultiWriter(w, f)
}

func init() {
	// Disable logrus output, which only comes from the docker
	// commandconn library that is used by buildkit's connhelper
	// and prints unneeded warning logs.
	logrus.StandardLogger().SetOutput(io.Discard)

	stdin = maybeWrapReader(os.Stdin, "DAGGER_LOG_STDIN")
	stdout = maybeWrapWriter(os.Stdout, "DAGGER_LOG_STDOUT")
	stderr = maybeWrapWriter(os.Stderr, "DAGGER_LOG_STDERR")

	rootCmd.AddCommand(
		listenCmd,
		versionCmd(),
		queryCmd,
		runCmd,
		watchCmd,
		configCmd,
		checksCmd,
		moduleInitCmd,
		moduleInstallCmd,
		moduleUnInstallCmd,
		moduleUpdateCmd,
		moduleDevelopCmd,
		modulePublishCmd,
		funcListCmd,
		callCoreCmd.Command(),
		callModCmd.Command(),
		sessionCmd(),
		newGenCmd(),
		shellCmd,
		clientCmd,
		mcpCmd,
	)

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
	Use:                   "dagger [options] [subcommand | file...]",
	Short:                 "A tool to run composable workflows in containers",
	SilenceErrors:         true, // handled in func main() instead
	DisableFlagsInUseLine: true,
	Args:                  cobra.ArbitraryArgs,
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

		checkForUpdates(cmd.Context(), cmd.ErrOrStderr())

		if err := checkCloudToken(cmd.Context(), cmd.OutOrStdout()); err != nil {
			return err
		}

		t.Capture(cmd.Context(), "cli_command", map[string]string{
			"name": commandName(cmd),
		})

		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 && !isFile(args[0]) {
			return fmt.Errorf("unknown command or file %q for %q%s", args[0], cmd.CommandPath(), findSuggestions(cmd, args[0]))
		}
		cmd.SetArgs(append([]string{"shell"}, args...))
		return cmd.Execute()
	},
}

func isFile(name string) bool {
	info, err := os.Stat(name)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func findSuggestions(c *cobra.Command, arg string) string {
	if c.DisableSuggestions {
		return ""
	}
	if c.SuggestionsMinimumDistance <= 0 {
		c.SuggestionsMinimumDistance = 2
	}
	var sb strings.Builder
	if suggestions := c.SuggestionsFor(arg); len(suggestions) > 0 {
		sb.WriteString("\n\nDid you mean this?\n")
		for _, s := range suggestions {
			_, _ = fmt.Fprintf(&sb, "\t%v\n", s)
		}
	}
	return sb.String()
}

func checkForUpdates(ctx context.Context, w io.Writer) {
	ctx, cancel := context.WithCancel(ctx)

	updateCh := make(chan string)
	go func() {
		defer close(updateCh)

		updateAvailable, err := updateAvailable(ctx)
		if err != nil {
			// Silently ignore the error -- it's already being caught by OTEL
			return
		}

		updateCh <- updateAvailable
	}()

	cobra.OnFinalize(func() {
		select {
		case updateAvailable := <-updateCh:
			if updateAvailable == "" {
				return
			}
			versionNag(w, updateAvailable)
		default:
			// If we didn't have enough time to check for updates,
			// cancel the update check.
			cancel()
		}
	})
}

// checkCloudToken triggers the Login flow if an invalid credentials file is detected.
func checkCloudToken(ctx context.Context, w io.Writer) error {
	_, err := auth.Token(ctx)
	var se *json.SyntaxError
	if errors.As(err, &se) {
		fmt.Fprint(w, "invalid credentials file, signing in...\n")
		return auth.Login(ctx, w)
	}
	return nil
}

func installGlobalFlags(flags *pflag.FlagSet) {
	flags.StringVar(&workdir, "workdir", ".", "Change the working directory")
	flags.CountVarP(&verbose, "verbose", "v", "Increase verbosity (use -vv or -vvv for more)")
	flags.CountVarP(&quiet, "quiet", "q", "Reduce verbosity (show progress, but clean up at the end)")
	flags.BoolVarP(&silent, "silent", "s", silent, "Do not show progress at all")
	flags.BoolVarP(&debugFlag, "debug", "d", debugFlag, "Show debug logs and full verbosity")
	flags.StringVar(&progress, "progress", "auto", "Progress output format (auto, plain, tty, dots)")
	flags.BoolVarP(&interactive, "interactive", "i", false, "Spawn a terminal on container exec failure")
	flags.StringVar(&interactiveCommand, "interactive-command", "/bin/sh", "Change the default command for interactive mode")
	flags.BoolVarP(&web, "web", "w", false, "Open trace URL in a web browser")
	flags.BoolVarP(&noExit, "no-exit", "E", false, "Leave the TUI running after completion")
	flags.BoolVarP(&autoApply, "auto-apply", "y", false, "Automatically apply changes when a changeset is returned")

	flags.StringVar(&dotOutputFilePath, "dot-output", "", "If set, write the calls made during execution to a dot file at the given path before exiting")
	flags.StringVar(&dotFocusField, "dot-focus-field", "", "In dot output, filter out vertices that aren't this field or descendents of this field")
	flags.BoolVar(&dotShowInternal, "dot-show-internal", false, "In dot output, if true then include calls and spans marked as internal")

	// this flag changes the behaviour of a few commands, e.g. call, functions, core, shell, etc.
	// all those functions will run in a remote cloud engine which gets created at execution time
	flags.BoolVar(&useCloudEngine, "cloud", useCloudEngine, "Run in a Dagger Cloud Engine")
	flags.Lookup("cloud").Hidden = true

	for _, fl := range []string{
		"workdir",
		"dot-output",
		"dot-focus-field",
		"dot-show-internal",
	} {
		if err := flags.MarkHidden(fl); err != nil {
			fmt.Fprintln(stdout, "Error hiding flag: "+fl, err)
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
	flags.ParseErrorsAllowlist.UnknownFlags = true
	installGlobalFlags(flags)
	if err := flags.Parse(os.Args[1:]); err != nil && !errors.Is(err, pflag.ErrHelp) {
		fmt.Fprintln(stderr, err)
		os.Exit(1)
	}
}

func Tracer() trace.Tracer {
	return otel.Tracer("dagger.io/cli")
}

func Resource(ctx context.Context) *resource.Resource {
	attrs := []attribute.KeyValue{
		semconv.ServiceName("dagger-cli"),
		semconv.ServiceVersion(engine.Version),
	}
	for k, v := range enginetel.LoadDefaultLabels(workdir, engine.Version) {
		attrs = append(attrs, attribute.String(k, v))
	}
	res, err := resource.New(ctx,
		resource.WithSchemaURL(semconv.SchemaURL),
		resource.WithAttributes(attrs...),
		resource.WithFromEnv(),
		resource.WithOSType(),
		resource.WithContainer(),
		resource.WithProcessCommandArgs(),
	)
	if err != nil {
		slog.Warn("failed to set up OTel resource", "error", err)
	}
	return res
}

// ExitError is an error that indicates a command should exit with a specific
// status code, without printing an error message, assuming a human readable
// message has been printed already.
//
// It is basically a shortcut for `os.Exit` while giving the TUI a chance to
// exit gracefully and flush output.
type ExitError struct {
	Code int

	// An optional originating error, for any code paths that go looking for it,
	// e.g. telemetry.End which looks for error origins.
	Original error
}

var Fail = ExitError{Code: 1}

func (e ExitError) Error() string {
	// Not actually printed anywhere.
	return fmt.Sprintf("exit code %d", e.Code)
}

func (e ExitError) Unwrap() error {
	return e.Original
}

const InstrumentationLibrary = "dagger.io/cli"

var opts dagui.FrontendOpts

func main() {
	parseGlobalFlags()
	opts.Verbosity += dagui.ShowCompletedVerbosity // keep progress by default
	opts.Verbosity += verbose                      // raise verbosity with -v
	opts.Verbosity -= quiet                        // lower verbosity with -q
	opts.Silent = silent                           // show no progress
	opts.Debug = debugFlag                         // show everything
	opts.RevealNoisySpans = reveal                 // disable 'reveal: true' mechanic (for tests)
	opts.OpenWeb = web
	opts.NoExit = noExit
	opts.DotOutputFilePath = dotOutputFilePath
	opts.DotFocusField = dotFocusField
	opts.DotShowInternal = dotShowInternal
	opts.UsingCloudEngine = useCloudEngine || strings.HasPrefix(RunnerHost, "dagger-cloud://")
	if progress == "auto" {
		if env := os.Getenv("DAGGER_PROGRESS"); env != "" {
			progress = env
		} else if os.Getenv("CLAUDECODE") == "1" {
			progress = "report"
		} else if hasTTY {
			progress = "tty"
		} else {
			progress = "plain"
		}
	}
	if silent {
		// if silent, don't even bother with the pretty frontend
		progress = "plain"
	}
	switch progress {
	case "plain":
		Frontend = idtui.NewPlain(stderr)
	case "tty":
		if !hasTTY {
			fmt.Fprintf(stderr, "no tty available for progress %q\n", progress)
			os.Exit(1)
		}
		Frontend = idtui.NewPretty(stderr)
	case "dots":
		Frontend = idtui.NewDots(stderr)
	case "report":
		Frontend = idtui.NewReporter(stderr)
	default:
		fmt.Fprintf(stderr, "unknown progress type %q\n", progress)
		os.Exit(1)
	}

	// Parse the interactive command to support shell-like syntax
	parsedCommand, err := shlex.Split(interactiveCommand)
	if err != nil {
		fmt.Fprintf(stderr, "cannot parse interactive command: %s", err)
		os.Exit(1)
	}
	interactiveCommandParsed = parsedCommand

	installGlobalFlags(rootCmd.PersistentFlags())

	ctx := context.Background()
	ctx = slog.ContextWithColorMode(ctx, termenv.EnvNoColor())
	ctx = slog.ContextWithDebugMode(ctx, debugFlag)
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)

	if err := rootCmd.ExecuteContext(ctx); err != nil {
		stop()
		var exit ExitError
		switch {
		case errors.As(err, &exit):
			os.Exit(exit.Code)
		case errors.Is(err, idtui.ErrShellExited):
			os.Exit(0)
		case errors.Is(err, context.Canceled) || errors.Is(err, idtui.ErrInterrupted):
			os.Exit(2)
		default:
			fmt.Fprintln(stderr, rootCmd.ErrPrefix(), err)
			var es interp.ExitStatus
			if errors.As(err, &es) {
				os.Exit(int(es))
			}
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
		workdir, err = pathutil.Getwd()
		if err != nil {
			return "", err
		}
	}
	workdir, err := pathutil.Abs(workdir)
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
	return wrapCmdDescription(c.Name(), c.Short, c.NamePadding())
}

func wrapCmdDescription(name, short string, padding int) string {
	width := getViewWidth()

	// Produce the same string length for all sibling commands by padding to
	// the right based on the longest name. Add two extra spaces to the left
	// of the screen, and three extra spaces before the description.
	nameFormat := fmt.Sprintf("  %%-%ds   ", padding)
	name = fmt.Sprintf(nameFormat, name)
	if len(name)+len(short) >= width {
		wrapped := wordwrap.String(short, width-len(name))
		indented := indent.String(wrapped, uint(len(name)))
		// first line shouldn't be indented since we're going to prepend the name
		short = strings.TrimLeftFunc(indented, unicode.IsSpace)
	}
	return name + short
}

func nameShortWrapped[S ~[]E, E any](s S, f func(e E) (string, string)) string {
	return nameShortWrappedIter(func(yield func(string, string) bool) {
		for _, v := range s {
			if !yield(f(v)) {
				return
			}
		}
	})
}

func nameShortWrappedIter(s iter.Seq2[string, string]) string {
	minPadding := 11
	maxLen := 0
	lines := []string{}

	for name, short := range Sorted2(s) {
		nameLen := len(name)
		if nameLen > maxLen {
			maxLen = nameLen
		}
		// This special character will be replaced with spacing once the
		// correct alignment is calculated
		lines = append(lines, fmt.Sprintf("%s\x00%s", name, short))
	}

	padding := max(minPadding, maxLen)

	sb := new(strings.Builder)
	for _, line := range lines {
		s := strings.SplitN(line, "\x00", 2)
		sb.WriteString(wrapCmdDescription(s[0], s[1], padding))
		sb.WriteString("\n")
	}
	return sb.String()
}

// Sorted2 returns a new Seq2 iterator ordered by the first element.
func Sorted2[K cmp.Ordered, V any](x iter.Seq2[K, V]) iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		m := maps.Collect(x)
		for _, k := range slices.Sorted(maps.Keys(m)) {
			if !yield(k, m[k]) {
				return
			}
		}
	}
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
