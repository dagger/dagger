package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"cuelang.org/go/cue"
	"github.com/google/go-github/github"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.dagger.io/dagger/cmd/dagger/cmd/common"
	"go.dagger.io/dagger/cmd/dagger/logger"
	"go.dagger.io/dagger/plan"
	"go.dagger.io/dagger/solver"
	"go.dagger.io/dagger/telemetry"
	"golang.org/x/oauth2"
	"golang.org/x/term"
)

func ghTest() {
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: os.Getenv("GITHUB_TOKEN")},
	)
	tc := oauth2.NewClient(context.Background(), ts)
	ghClient := github.NewClient(tc)

	sha := os.Getenv("GITHUB_SHA")
	ref := os.Getenv("GITHUB_REF")

	if os.Getenv("GITHUB_TOKEN") != "" {
		fmt.Printf("GOT TOKEN\n")
	} else {
		fmt.Printf("NO TOKEN\n")
	}
	fmt.Printf("SHA: %s\n", sha)
	fmt.Printf("REF: %s\n", ref)

	status := "in_progress"
	cr, _, err := ghClient.Checks.CreateCheckRun(context.TODO(), "dagger", "dagger", github.CreateCheckRunOptions{
		Name:       "dagger",
		HeadBranch: ref,
		HeadSHA:    sha,
		Status:     &status,
	})
	if err != nil {
		panic(err)
	}
	go func() {
		time.Sleep(10 * time.Second)
		status = "completed"
		cr, _, err = ghClient.Checks.UpdateCheckRun(context.TODO(), "dagger", "dagger", cr.GetID(), github.UpdateCheckRunOptions{
			Status: &status,
		})
		if err != nil {
			panic(err)
		}
	}()
}

var doCmd = &cobra.Command{
	Use:   "do [OPTIONS] ACTION [SUBACTION...]",
	Short: "Execute a dagger action.",
	PreRun: func(cmd *cobra.Command, args []string) {
		// Fix Viper bug for duplicate flags:
		// https://github.com/spf13/viper/issues/233
		if err := viper.BindPFlags(cmd.Flags()); err != nil {
			panic(err)
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) < 1 {
			doHelpCmd(cmd, nil)
			return
		}

		ghTest()

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
		target := getTargetPath(args)

		doneCh := common.TrackCommand(ctx, cmd, &telemetry.Property{
			Name:  "action",
			Value: target.String(),
		})

		err = cl.Do(ctx, p.Context(), func(ctx context.Context, s *solver.Solver) error {
			return p.Do(ctx, target, s)
		})

		<-doneCh

		p.Context().TempDirs.Clean()

		if err != nil {
			lg.Fatal().Err(err).Msg("failed to execute plan")
		}
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

func doHelpCmd(cmd *cobra.Command, _ []string) {
	lg := logger.New()

	fmt.Println(cmd.Short)

	err := printActions(os.Stdout, getTargetPath(cmd.Flags().Args()))

	fmt.Printf("\n%s", cmd.UsageString())

	if err != nil {
		lg.Fatal().Err(err).Msg("failed to load plan")
	}
}

func printActions(w io.Writer, target cue.Path) error {
	p, err := loadPlan()
	if err != nil {
		return err
	}

	action := p.Action().FindByPath(target)
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
	doCmd.Flags().StringArray("cache-to", []string{},
		"Cache export destinations (eg. user/app:cache, type=local,dest=path/to/dir)")
	doCmd.Flags().StringArray("cache-from", []string{},
		"External cache sources (eg. user/app:cache, type=local,src=path/to/dir)")

	doCmd.SetHelpFunc(doHelpCmd)

	if err := viper.BindPFlags(doCmd.Flags()); err != nil {
		panic(err)
	}
}
