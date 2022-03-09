package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"cuelang.org/go/cue"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.dagger.io/dagger/cmd/dagger/cmd/common"
	"go.dagger.io/dagger/cmd/dagger/logger"
	"go.dagger.io/dagger/plan"
	"go.dagger.io/dagger/solver"
	"go.dagger.io/dagger/telemetry"
	"golang.org/x/term"
)

var doCmd = &cobra.Command{
	Use:   "do [OPTIONS] ACTION [SUBACTION...]",
	Short: "Execute a dagger action.",
	// Args:  cobra.MinimumNArgs(1),
	PreRun: func(cmd *cobra.Command, args []string) {
		// Fix Viper bug for duplicate flags:
		// https://github.com/spf13/viper/issues/233
		if err := viper.BindPFlags(cmd.Flags()); err != nil {
			panic(err)
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) < 1 {
			doHelp(cmd, nil)
			return
		}

		var (
			lg  = logger.New()
			tty *logger.TTYOutput
			err error
		)

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

		p, err := loadPlan()
		if err != nil {
			lg.Fatal().Err(err).Msg("failed to load plan")
		}

		doneCh := common.TrackCommand(ctx, cmd, &telemetry.Property{
			Name:  "action",
			Value: p.Action().Path.String(),
		})

		err = cl.Do(ctx, p.Context(), func(ctx context.Context, s solver.Solver) error {
			return p.Do(ctx, getTargetPath(args), s)
		})

		if err != nil {
			lg.Fatal().Err(err).Msg("failed to execute plan")
		}
		<-doneCh
	},
}

func loadPlan() (*plan.Plan, error) {
	planPath := viper.GetString("plan")

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

func doHelp(cmd *cobra.Command, _ []string) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 1, ' ', tabwriter.StripEscape)
	defer w.Flush()

	planPath := viper.GetString("plan")

	var (
		errorMsg            string
		loadedMsg           string
		actionLookupPathMsg string
		action              *plan.Action
		actions             []*plan.Action
	)

	p, err := loadPlan()
	if err != nil {
		errorMsg = "Error: failed to load plan\n\n"
	} else {
		loadedMsg = "Plan loaded from " + planPath
		actionLookupPath := getTargetPath(cmd.Flags().Args())
		action = p.Action().FindByPath(actionLookupPath)
		if action == nil {
			errorMsg = "Error: action not found\n\n"
		} else {
			actions = action.Children
			actionLookupPathMsg = fmt.Sprintf(`%s:`, actionLookupPath.String())
		}
	}
	fmt.Printf(`%s%s

%s

%s

%s
`, errorMsg, cmd.Short, cmd.UsageString(), loadedMsg, actionLookupPathMsg)

	// fmt.Fprintln(w, "Actions\tDescription\tPackage")
	// fmt.Fprintln(w, "\t\t")
	for _, a := range actions {
		if !a.Hidden {
			lineParts := []string{"", a.Name, strings.TrimSpace(a.Comment)}
			fmt.Fprintln(w, strings.Join(lineParts, "\t"))
		}
	}
}

func init() {
	doCmd.Flags().StringArrayP("with", "w", []string{}, "")
	doCmd.Flags().StringP("plan", "p", ".", "Path to plan (defaults to current directory)")

	doCmd.SetHelpFunc(doHelp)

	if err := viper.BindPFlags(doCmd.Flags()); err != nil {
		panic(err)
	}
}
