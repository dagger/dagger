package input

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"go.dagger.io/dagger/cmd/dagger/cmd/common"
	"go.dagger.io/dagger/cmd/dagger/logger"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/environment"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
	"go.dagger.io/dagger/state"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var listCmd = &cobra.Command{
	Use:   "list [TARGET] [flags]",
	Short: "List the inputs of an environment",
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

		project := common.CurrentProject(ctx)
		st := common.CurrentEnvironmentState(ctx, project)

		lg = lg.With().
			Str("environment", st.Name).
			Logger()

		doneCh := common.TrackProjectCommand(ctx, cmd, project, st)

		env, err := environment.New(st)
		if err != nil {
			lg.Fatal().Err(err).Msg("unable to create environment")
		}

		cl := common.NewClient(ctx)
		err = cl.Do(ctx, env.Context(), func(ctx context.Context, s solver.Solver) error {
			inputs, err := env.ScanInputs(ctx, false)
			if err != nil {
				return err
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "Input\tValue\tSet by user\tDescription")

			for _, inp := range inputs {
				isConcrete := (inp.IsConcreteR() == nil)
				_, hasDefault := inp.Default()

				switch {
				case plancontext.IsSecretValue(inp):
					if _, err := env.Context().Secrets.FromValue(inp); err != nil {
						isConcrete = false
					}
				case plancontext.IsFSValue(inp):
					if _, err := env.Context().FS.FromValue(inp); err != nil {
						isConcrete = false
					}
				case plancontext.IsServiceValue(inp):
					if _, err := env.Context().Services.FromValue(inp); err != nil {
						isConcrete = false
					}
				}

				if !viper.GetBool("all") {
					// skip input that is not overridable
					if !hasDefault && isConcrete {
						continue
					}
				}

				if !viper.GetBool("show-optional") && !viper.GetBool("all") {
					// skip input if there is already a default value
					if hasDefault {
						continue
					}
				}

				fmt.Fprintf(w, "%s\t%s\t%t\t%s\n",
					inp.Path(),
					common.FormatValue(inp),
					isUserSet(st, inp),
					common.ValueDocOneLine(inp),
				)
			}

			w.Flush()
			return nil
		})

		<-doneCh

		if err != nil {
			lg.Fatal().Err(err).Msg("failed to query environment")
		}

	},
}

func isUserSet(env *state.State, val *compiler.Value) bool {
	for key := range env.Inputs {
		if val.Path().String() == key {
			return true
		}
	}

	return false
}

func init() {
	listCmd.Flags().BoolP("all", "a", false, "List all inputs (include non-overridable)")
	listCmd.Flags().Bool("show-optional", false, "List optional inputs (those with default values)")

	if err := viper.BindPFlags(listCmd.Flags()); err != nil {
		panic(err)
	}
}
