package output

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"go.dagger.io/dagger/client"
	"go.dagger.io/dagger/cmd/dagger/cmd/common"
	"go.dagger.io/dagger/cmd/dagger/logger"
	"go.dagger.io/dagger/environment"
	"go.dagger.io/dagger/solver"
	"go.dagger.io/dagger/state"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var listCmd = &cobra.Command{
	Use:   "list [TARGET] [flags]",
	Short: "List the outputs of an environment",
	Args:  cobra.MaximumNArgs(1),
	PreRun: func(cmd *cobra.Command, args []string) {
		// Fix Viper bug for duplicate flags:
		// https://github.com/spf13/viper/issues/233
		if err := viper.BindPFlags(cmd.Flags()); err != nil {
			panic(err)
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		lg := logger.New()
		ctx := lg.WithContext(cmd.Context())

		workspace := common.CurrentWorkspace(ctx)
		st := common.CurrentEnvironmentState(ctx, workspace)

		ListOutputs(ctx, st, true)
	},
}

func ListOutputs(ctx context.Context, st *state.State, isTTY bool) {
	lg := log.Ctx(ctx).With().
		Str("environment", st.Name).
		Logger()

	c, err := client.New(ctx, "", false)
	if err != nil {
		lg.Fatal().Err(err).Msg("unable to create client")
	}
	defer c.Close()

	_, err = c.Do(ctx, st, func(ctx context.Context, env *environment.Environment, s solver.Solver) error {
		outputs, err := env.ScanOutputs(ctx)
		if err != nil {
			return err
		}

		if !isTTY {
			for _, out := range outputs {
				lg.Info().Str("name", out.Path().String()).
					Str("value", fmt.Sprintf("%v", out.Cue())).
					Msg("output")
			}
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "Output\tValue\tDescription")

		for _, out := range outputs {
			fmt.Fprintf(w, "%s\t%s\t%s\n",
				out.Path(),
				common.FormatValue(out),
				common.ValueDocOneLine(out),
			)
		}

		w.Flush()
		return nil
	})

	if err != nil {
		lg.Fatal().Err(err).Msg("failed to query environment")
	}
}
