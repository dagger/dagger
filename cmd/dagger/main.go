package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/pprof"
	"runtime/trace"
	"strings"
	"unicode"

	"github.com/dagger/dagger/analytics"
	"github.com/dagger/dagger/tracing"
	"github.com/muesli/reflow/indent"
	"github.com/muesli/reflow/wordwrap"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/term"
)

var (
	execGroup = &cobra.Group{
		ID:    "exec",
		Title: "Execution Commands",
	}

	workdir string

	cpuprofile string
	pprofAddr  string
	debug      bool
)

func init() {
	// Disable logrus output, which only comes from the docker
	// commandconn library that is used by buildkit's connhelper
	// and prints unneeded warning logs.
	logrus.StandardLogger().SetOutput(io.Discard)

	rootCmd.PersistentFlags().StringVar(&workdir, "workdir", ".", "The host workdir loaded into dagger")
	rootCmd.PersistentFlags().BoolVar(&debug, "debug", false, "Show more information for debugging")
	rootCmd.PersistentFlags().StringVar(&cpuprofile, "cpuprofile", "", "collect CPU profile to path, and trace at path.trace")
	rootCmd.PersistentFlags().StringVar(&pprofAddr, "pprof", "", "serve HTTP pprof at this address")

	for _, fl := range []string{"workdir", "cpuprofile", "pprof"} {
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
		moduleCmd,
		sessionCmd(),
		newGenCmd(),
		downloadCmd,
		upCmd,
		shellCmd,
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

			tracePath := cpuprofile + ".trace"

			traceF, err := os.Create(tracePath)
			if err != nil {
				return fmt.Errorf("create trace: %w", err)
			}

			if err := trace.Start(traceF); err != nil {
				return fmt.Errorf("start trace: %w", err)
			}
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

		t := analytics.New(analytics.DoNotTrack())
		cmd.SetContext(analytics.WithContext(cmd.Context(), t))

		t.Capture(cmd.Context(), "cli_command", map[string]any{
			"name": commandName(cmd),
		})

		return nil
	},
	PersistentPostRun: func(cmd *cobra.Command, args []string) {
		pprof.StopCPUProfile()
		trace.Stop()
		analytics.Ctx(cmd.Context()).Close()
	},
}

func main() {
	closer := tracing.Init()
	if err := rootCmd.Execute(); err != nil {
		closer.Close()
		os.Exit(1)
	}
	closer.Close()
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
