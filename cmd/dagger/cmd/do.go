package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"cuelang.org/go/cue"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"go.dagger.io/dagger/cmd/dagger/cmd/common"
	"go.dagger.io/dagger/cmd/dagger/logger"
	"go.dagger.io/dagger/plan"
	"go.dagger.io/dagger/solver"
	"go.dagger.io/dagger/telemetry"
	"golang.org/x/term"
)

var doCmd = &cobra.Command{
	Use: "do ACTION [SUBACTION...]",
	// Short: "Execute a dagger action.",
	PreRun: func(cmd *cobra.Command, args []string) {
		// Fix Viper bug for duplicate flags:
		// https://github.com/spf13/viper/issues/233
		if err := viper.BindPFlags(cmd.Flags()); err != nil {
			panic(err)
		}
	},
	// Don't fail on unknown flags for the first parse
	FParseErrWhitelist: cobra.FParseErrWhitelist{
		UnknownFlags: true,
	},
	// We're going to take care of flag parsing ourselves
	DisableFlagParsing: true,
	Run: func(cmd *cobra.Command, args []string) {
		cmd.Flags().Parse(args)
		if err := viper.BindPFlags(cmd.Flags()); err != nil {
			panic(err)
		}

		var (
			lg  = logger.New()
			tty *logger.TTYOutput
			err error
		)

		switch !viper.GetBool("experimental") {
		case len(viper.GetString("platform")) > 0:
			lg.Fatal().Err(err).Msg("--platform requires --experimental flag")
		}

		targetPath := getTargetPath(cmd.Flags().Args())

		daggerPlan, err := loadPlan(viper.GetString("plan"))
		if err != nil {
			if viper.GetBool("help") {
				doHelpCmd(cmd, nil, nil, nil, targetPath, nil)
				os.Exit(0)
			}
			err = fmt.Errorf("failed to load plan: %w", err)
			doHelpCmd(cmd, nil, nil, nil, targetPath, []string{err.Error()})
			os.Exit(1)
		}

		action := daggerPlan.Action().FindByPath(targetPath)

		if action == nil {
			selectorStrs := []string{}
			for _, selector := range targetPath.Selectors()[1:] {
				selectorStrs = append(selectorStrs, selector.String())
			}
			targetStr := strings.Join(selectorStrs, " ")

			err = fmt.Errorf("action not found: %s", targetStr)
			// Find closest action
			action = daggerPlan.Action().FindClosest(targetPath)
			targetPath = action.Path
			doHelpCmd(cmd, daggerPlan, action, nil, action.Path, []string{err.Error()})
			os.Exit(1)
		}

		actionFlags := getActionFlags(action)

		cmd.Flags().AddFlagSet(actionFlags)

		cmd.Flags().ParseErrorsWhitelist = pflag.ParseErrorsWhitelist{
			UnknownFlags: false,
		}
		err = cmd.Flags().Parse(args)
		if err != nil {
			doHelpCmd(cmd, daggerPlan, action, actionFlags, targetPath, []string{err.Error()})
			os.Exit(1)
		}

		if err := viper.BindPFlags(cmd.Flags()); err != nil {
			panic(err)
		}

		if len(cmd.Flags().Args()) < 1 || viper.GetBool("help") {
			doHelpCmd(cmd, daggerPlan, action, actionFlags, targetPath, []string{})
			os.Exit(0)
		}

		if f := viper.GetString("log-format"); f == "tty" || f == "auto" && term.IsTerminal(int(os.Stdout.Fd())) {
			tty, err = logger.NewTTYOutput(os.Stderr)
			if err != nil {
				lg.Fatal().Err(err).Msg("failed to initialize TTY logger")
			}
			tty.Start()
			defer tty.Stop()

			lg = lg.Output(tty)
		}

		ctx := lg.WithContext(cmd.Context())
		cl := common.NewClient(ctx)

		actionFlags.VisitAll(func(flag *pflag.Flag) {
			if cmd.Flags().Changed(flag.Name) {
				fmt.Printf("Changed: %s: %s\n", flag.Name, cmd.Flags().Lookup(flag.Name).Value.String())
				flagPath := []cue.Selector{}
				flagPath = append(flagPath, targetPath.Selectors()...)
				flagPath = append(flagPath, cue.Str(flag.Name))
				daggerPlan.Source().FillPath(cue.MakePath(flagPath...), viper.Get(flag.Name))
			}
		})

		doneCh := common.TrackCommand(ctx, cmd, &telemetry.Property{
			Name:  "action",
			Value: targetPath.String(),
		})

		err = cl.Do(ctx, daggerPlan.Context(), func(ctx context.Context, s *solver.Solver) error {
			return daggerPlan.Do(ctx, targetPath, s)
		})

		<-doneCh

		daggerPlan.Context().TempDirs.Clean()

		if err != nil {
			lg.Fatal().Err(err).Msg("failed to execute plan")
		}
	},
}

func loadPlan(planPath string) (*plan.Plan, error) {
	// support only local filesystem paths
	// even though CUE supports loading module and package names
	absPlanPath, err := filepath.Abs(planPath)
	if err != nil {
		return nil, err
	}

	_, err = os.Stat(absPlanPath)
	if err != nil {
		return nil, err
	}

	return plan.Load(context.Background(), plan.Config{
		Args: []string{planPath},
		With: viper.GetStringSlice("with"),
	})
}

func getTargetPath(args []string) cue.Path {
	selectors := []cue.Selector{plan.ActionSelector}
	for _, arg := range args {
		selectors = append(selectors, cue.Str(arg))
	}
	return cue.MakePath(selectors...)
}

func doHelpCmd(cmd *cobra.Command, daggerPlan *plan.Plan, action *plan.Action, actionFlags *pflag.FlagSet, target cue.Path, preamble []string) {
	lg := logger.New()

	if len(preamble) > 0 {
		fmt.Println(strings.Join(preamble, "\n"))
		fmt.Println()
	}

	target = cue.MakePath(target.Selectors()[1:]...)

	if action != nil {
		selectorStrs := []string{}
		for _, selector := range target.Selectors() {
			selectorStrs = append(selectorStrs, selector.String())
		}
		targetStr := strings.Join(selectorStrs, " ")
		fmt.Printf("Usage: \n  dagger do %s [flags]\n\n", targetStr)
		if actionFlags != nil {
			fmt.Println("Options")
			actionFlags.VisitAll(func(flag *pflag.Flag) {
				flag.Hidden = false
			})
			fmt.Println(actionFlags.FlagUsages())
			actionFlags.VisitAll(func(flag *pflag.Flag) {
				flag.Hidden = true
			})
		}
	} else {
		fmt.Println("Usage: \n  dagger do [flags]")
	}

	var err error
	if daggerPlan != nil {
		err = printActions(daggerPlan, action, os.Stdout, target)
	}

	fmt.Printf("\n%s", cmd.UsageString())

	if err == nil {
		lg.Fatal().Err(err)
	}
}

func getActionFlags(action *plan.Action) *pflag.FlagSet {
	flags := pflag.NewFlagSet("action inputs", pflag.ContinueOnError)
	flags.Usage = func() {}

	if action == nil {
		panic("action is nil")
	}

	if action.Inputs() == nil {
		panic("action inputs is nil")
	}

	for _, input := range action.Inputs() {
		switch input.Type {
		case "string":
			flags.String(input.Name, "", input.Documentation)
		case "int":
			flags.Int(input.Name, 0, input.Documentation)
		case "bool":
			flags.Bool(input.Name, false, input.Documentation)
		case "float":
			flags.Float64(input.Name, 0, input.Documentation)
		case "number":
			flags.Float64(input.Name, 0, input.Documentation)
		default:
		}
		flags.MarkHidden(input.Name)
	}
	return flags
}

func printActions(p *plan.Plan, action *plan.Action, w io.Writer, target cue.Path) error {
	if p == nil {
		return nil
	}

	if action == nil {
		return fmt.Errorf("action %s not found", target.String())
	}

	if len(action.Name) < 1 {
		return nil
	}

	fmt.Printf("\nAvailable Actions:\n")
	tw := tabwriter.NewWriter(w, 0, 0, 1, ' ', tabwriter.StripEscape)
	defer tw.Flush()

	for _, a := range action.Children {
		if !a.Hidden {
			lineParts := []string{"", a.Name, a.Documentation}
			fmt.Fprintln(tw, strings.Join(lineParts, "\t"))
		}
	}

	return nil
}

func init() {
	doCmd.Flags().StringArrayP("with", "w", []string{}, "")
	doCmd.Flags().StringP("plan", "p", ".", "Path to plan (defaults to current directory)")
	doCmd.Flags().Bool("no-cache", false, "Disable caching")
	doCmd.Flags().String("platform", "", "Set target build platform (requires experimental)")
	doCmd.Flags().StringArray("cache-to", []string{},
		"Cache export destinations (eg. user/app:cache, type=local,dest=path/to/dir)")
	doCmd.Flags().StringArray("cache-from", []string{},
		"External cache sources (eg. user/app:cache, type=local,src=path/to/dir)")

	doCmd.SetUsageTemplate(`{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [command]{{end}}{{if gt (len .Aliases) 0}}
Aliases:
  {{.NameAndAliases}}{{end}}{{if .HasExample}}
Examples:
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}
Available Commands:{{range .Commands}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}
Flags:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}
Global Flags:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasHelpSubCommands}}
Additional help topics:{{range .Commands}}{{if .IsAdditionalHelpTopicCommand}}
  {{rpad .CommandPath .CommandPathPadding}} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableSubCommands}}
Use "{{.CommandPath}} [command] --help" for more information about a command.{{end}}
`)
}
