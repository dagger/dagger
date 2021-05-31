package output

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"go.dagger.io/dagger/client"
	"go.dagger.io/dagger/cmd/dagger/cmd/common"
	"go.dagger.io/dagger/cmd/dagger/logger"
	"go.dagger.io/dagger/environment"
	"go.dagger.io/dagger/solver"

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

		lg = lg.With().
			Str("environment", st.Name).
			Logger()

		c, err := client.New(ctx, "", false)
		if err != nil {
			lg.Fatal().Err(err).Msg("unable to create client")
		}

		_, err = c.Do(ctx, st, func(ctx context.Context, env *environment.Environment, s solver.Solver) error {
			outputs, err := env.ScanOutputs(ctx)
			if err != nil {
				return err
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "Output\tType\tValue\tDescription")

			for _, out := range outputs {
				valStr := "-"

				if out.IsConcreteR() == nil {
					valStr, err = out.Cue().String()
					if err != nil {
						return err
					}
				} else if !viper.GetBool("all") {
					continue
				}

				valStr = strings.ReplaceAll(valStr, "\n", "\\n")

				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
					out.Path(),
					common.ValueType(out),
					valStr,
					common.ValueDocString(out),
				)
			}

			w.Flush()
			return nil
		})

		if err != nil {
			lg.Fatal().Err(err).Msg("failed to query environment")
		}
	},
}

func init() {
	listCmd.Flags().BoolP("all", "a", false, "List all outputs (include non-concrete)")

	if err := viper.BindPFlags(listCmd.Flags()); err != nil {
		panic(err)
	}
}
