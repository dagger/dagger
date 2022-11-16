package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/format"
	"github.com/containerd/console"
	"github.com/mitchellh/go-homedir"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"go.dagger.io/dagger/analytics"
	"go.dagger.io/dagger/cmd/dagger/cmd/common"
	"go.dagger.io/dagger/cmd/dagger/logger"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plan"
	"go.dagger.io/dagger/solver"
	"go.dagger.io/dagger/telemetry"
	"go.dagger.io/dagger/telemetry/event"
	"golang.org/x/sync/errgroup"
	"golang.org/x/term"
)

var experimentalFlags = []string{
	"platform",
	"dry-run",
	"telemetry-log",
}

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
			tm   = telemetry.New()
			lg   = logger.NewWithCloud(tm)
			ctx  = lg.WithContext(cmd.Context())
			tty  *logger.TTYOutput
			tty2 *logger.TTYOutputV2
		)

		if !viper.GetBool("experimental") {
			for _, f := range experimentalFlags {
				if viper.IsSet(f) {
					lg.Fatal().Msg(fmt.Sprintf("--%s requires --experimental flag", f))
				}
			}
		}

		// Enable telemetry op logging to file
		if viper.GetBool("telemetry-log") {
			tm.EnableLogToFile()
		}

		targetPath := getTargetPath(cmd.Flags().Args())

		daggerPlan, err := loadPlan(ctx, viper.GetString("plan"))

		targetAction := cue.MakePath(targetPath.Selectors()[1:]...).String()

		if !viper.GetBool("help") && (err != nil || len(targetAction) > 0) {
			// we send the RunStarted event regardless if `loadPlan` fails since we also want to capture
			// and provide assistance when plan fails to evaluate
			var p string
			var validationErr *plan.ErrorValidation
			if daggerPlan != nil {
				p = fmt.Sprintf("%#v", daggerPlan.Source().Cue())
			} else if errors.As(err, &validationErr) {
				p = fmt.Sprintf("%#v", validationErr.Plan.Source().Cue())
			}
			// Fire "run started" event once we know there is an action to run (ie. not calling --help)
			tm.Push(ctx, event.RunStarted{
				Action: targetAction,
				Args:   os.Args[1:],
				Plan:   p,
			})

			// we need to flush events here since otherwise a "run.completed" event
			// could arrive before the "run.started" and the run will be processed
			// incorrectly.
			tm.Flush()
			rid := tm.RunID()
			tm = telemetry.New()
			tm.SetRunID(rid)
			ctx = tm.WithContext(ctx)
			if viper.GetBool("telemetry-log") {
				tm.EnableLogToFile()
			}
		}

		defer tm.Flush()

		if err != nil {
			lg.Error().Err(err).Msgf("failed to load plan")

			if viper.GetBool("help") {
				doHelpCmd(cmd, nil, nil, nil, targetPath, []string{err.Error()})
				os.Exit(0)
			} else {
				captureRunCompletedFailed(ctx, tm, err)
			}

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

			captureRunCompletedFailed(ctx, tm, err)
			// Find closest action
			action = daggerPlan.Action().FindClosest(targetPath)
			if action == nil {
				err = fmt.Errorf("no action found")
				doHelpCmd(cmd, nil, nil, nil, targetPath, []string{err.Error()})
				os.Exit(1)
			}

			targetPath = action.Path
			doHelpCmd(cmd, daggerPlan, action, nil, action.Path, []string{err.Error()})
			os.Exit(1)
		}

		actionFlags := getActionFlags(action)
		cmd.Flags().AddFlagSet(actionFlags)

		// clear slice flags to avoid duplication
		// https://github.com/spf13/pflag/issues/244
		cmd.Flags().VisitAll(func(f *pflag.Flag) {
			if v, ok := f.Value.(pflag.SliceValue); ok {
				v.Replace([]string{})
			}
		})

		err = cmd.Flags().Parse(args)

		if err != nil {
			captureRunCompletedFailed(ctx, tm, err)
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

		f := viper.GetString("log-format")
		switch {
		case f == "tty" || f == "auto" && term.IsTerminal(int(os.Stdout.Fd())):
			tty, err = logger.NewTTYOutput(os.Stderr)
			if err != nil {
				captureRunCompletedFailed(ctx, tm, err)
				lg.Fatal().Err(err).Msg("failed to initialize TTY logger")
			}
			tty.Start()
			defer tty.Stop()

			lg = lg.Output(logger.TeeCloud(tm, tty))
			ctx = lg.WithContext(ctx)

		case f == "tty2":
			// FIXME: dolanor: remove once it's more stable/debuggable
			f, err := ioutil.TempFile("/tmp", "dagger-console-*.log")
			if err != nil {
				lg.Fatal().Err(err).Msg("failed to create TTY file logger")
			}
			defer func() {
				err := f.Close()
				if err != nil {
					lg.Fatal().Err(err).Msg("failed to close TTY file logger")
				}
			}()

			cons, err := console.ConsoleFromFile(os.Stderr)
			if err != nil {
				lg.Fatal().Err(err).Msg("failed to create TTY console")
			}

			c := logger.ConsoleAdapter{Cons: cons, F: f}
			tty2, err = logger.NewTTYOutputConsole(&c)
			if err != nil {
				lg.Fatal().Err(err).Msg("failed to initialize TTYv2 logger")
			}
			tty2.Start()
			defer tty2.Stop()

			lg = lg.Output(logger.TeeCloud(tm, tty2))
			ctx = lg.WithContext(ctx)

		}

		// cl := common.NewClient(ctx)

		actionFlags.VisitAll(func(flag *pflag.Flag) {
			if cmd.Flags().Changed(flag.Name) {
				fmt.Printf("Changed: %s: %s\n", flag.Name, cmd.Flags().Lookup(flag.Name).Value.String())
				flagPath := []cue.Selector{}
				flagPath = append(flagPath, targetPath.Selectors()...)
				flagPath = append(flagPath, cue.Str(flag.Name))
				daggerPlan.Source().FillPath(cue.MakePath(flagPath...), viper.Get(flag.Name))
			}
		})

		doneCh := common.TrackCommand(ctx, cmd, &analytics.Property{
			Name:  "action",
			Value: cue.MakePath(targetPath.Selectors()[1:]...).String(),
		})

		eg, gctx := errgroup.WithContext(ctx)

		// err = cl.Do(ctx, daggerPlan.Context(), func(ctx context.Context, s *solver.Solver) error {
		// 	return daggerPlan.Do(ctx, targetPath, s)
		// })

		eg.Go(func() error {
			s := &solver.Solver{}
			return daggerPlan.Do(gctx, targetPath, s)
		})

		err = eg.Wait()
		if err != nil {
			lg.Fatal().Err(err).Msg("failed to execute plan")
		}

		<-doneCh

		daggerPlan.Context().TempDirs.Clean()

		if err != nil {
			captureRunCompletedFailed(ctx, tm, err)
			lg.Fatal().Err(err).Msg("failed to execute plan")
		}

		format := viper.GetString("output-format")
		file := viper.GetString("output")

		if format == "" && !term.IsTerminal(int(os.Stdout.Fd())) {
			format = "json"
		}

		if file == "" && tty != nil {
			// stop tty logger because we're about to print to stdout for the outputs
			tty.Stop()
			lg = logger.NewWithCloud(tm)
		}

		action.UpdateFinal(daggerPlan.Final())

		outputs := map[string]string{}
		for _, f := range action.Outputs() {
			key := f.Selector.String()
			var value interface{}
			if err := json.Unmarshal([]byte(f.Value.JSON().String()), &value); err != nil {
				lg.Error().Err(err).Msg("failed to marshal output")
				continue
			}

			outputs[key] = fmt.Sprintf("%v", value)
		}

		if err := plan.PrintOutputs(action.Outputs(), format, file); err != nil {
			captureRunCompletedFailed(ctx, tm, err)
			lg.Fatal().Err(err).Msg("failed to print action outputs")
		}

		tm.Push(ctx, event.RunCompleted{
			State:   event.RunCompletedStateSuccess,
			Outputs: outputs,
		})
	},
}

func loadPlan(ctx context.Context, planPath string) (*plan.Plan, error) {
	// support only local filesystem paths
	// even though CUE supports loading module and package names
	homedirPlanPathExpanded, err := homedir.Expand(planPath)
	if err != nil {
		return nil, err
	}

	absPlanPath, err := filepath.Abs(homedirPlanPathExpanded)
	if err != nil {
		return nil, err
	}

	_, err = os.Stat(absPlanPath)
	if err != nil {
		return nil, err
	}

	return plan.Load(ctx, plan.Config{
		Args:   []string{planPath},
		With:   viper.GetStringSlice("with"),
		DryRun: viper.GetBool("dry-run"),
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
		if targetStr == "" {
			targetStr = "<action> [subaction...]"
		}

		fmt.Printf("Usage: \n  dagger-cue do %s [flags]\n\n", targetStr)
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
		fmt.Println("Usage: \n  dagger-cue do <action> [subaction...] [flags]")
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
	flags.ParseErrorsWhitelist = pflag.ParseErrorsWhitelist{
		UnknownFlags: true,
	}
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

func captureRunCompletedFailed(ctx context.Context, tm *telemetry.Telemetry, err error) {
	var instanceErr *compiler.ErrorInstance
	rce := event.RunCompleted{
		State: event.RunCompletedStateFailed,
		Err:   &event.RunError{Message: err.Error()},
		Error: err.Error(),
	}

	if errors.As(err, &instanceErr) {
		planFiles := map[string]string{}
		for _, f := range instanceErr.Instance.Files {
			bfile, _ := format.Node(f)
			planFiles[filepath.Base(f.Filename)] = string(bfile)
		}
		rce.Err.PlanFiles = planFiles
	}
	tm.Push(ctx, rce)

	// manually flush events since otherwise they could be skipped as
	// the program is exiting.
	tm.Flush()
}

func init() {
	doCmd.Flags().StringArrayP("with", "w", []string{}, "")
	doCmd.Flags().StringP("plan", "p", ".", "Path to plan (defaults to current directory)")
	doCmd.Flags().Bool("dry-run", false, "Dry run mode")
	doCmd.Flags().Bool("no-cache", false, "Disable caching")
	doCmd.Flags().Bool("telemetry-log", false, "Send telemetry logs to file (requires experimental)")
	doCmd.Flags().String("platform", "", "Set target build platform (requires experimental)")
	doCmd.Flags().String("output", "", "File path to write the action's output values. Prints to stdout if empty")
	doCmd.Flags().String("output-format", "", "Format for output values (plain, json, yaml)")
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
