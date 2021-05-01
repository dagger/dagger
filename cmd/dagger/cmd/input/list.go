package input

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"dagger.io/go/cmd/dagger/cmd/common"
	"dagger.io/go/cmd/dagger/logger"
	"dagger.io/go/dagger"

	"cuelang.org/go/cue/ast"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var listCmd = &cobra.Command{
	Use:   "list [TARGET] [flags]",
	Short: "List for the inputs of an environment",
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

		store, err := dagger.DefaultStore()
		if err != nil {
			lg.Fatal().Err(err).Msg("failed to load store")
		}

		environment := common.GetCurrentEnvironmentState(ctx, store)

		// print any persisted inputs
		if len(environment.Inputs) > 0 {
			fmt.Println("Saved Inputs:")
			for _, input := range environment.Inputs {
				// Todo, how to pull apart an input to print relevant information
				fmt.Printf("%s: %v\n", input.Key, input.Value)
			}
			// add some space
			fmt.Println()
		}

		lg = lg.With().
			Str("environmentName", environment.Name).
			Str("environmentId", environment.ID).
			Logger()

		c, err := dagger.NewClient(ctx, "", false)
		if err != nil {
			lg.Fatal().Err(err).Msg("unable to create client")
		}

		_, err = c.Do(ctx, environment, func(lCtx context.Context, lDeploy *dagger.Environment, lSolver dagger.Solver) error {
			inputs, err := lDeploy.ScanInputs()
			if err != nil {
				return err
			}

			fmt.Println("Plan Inputs:")
			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "Path\tType")

			for _, val := range inputs {
				// check for references
				// this is here because it has issues
				// so we wrap it in a flag to control its usage while debugging
				_, vals := val.Expr()
				if !viper.GetBool("keep-references") {
					foundRef := false
					for _, ve := range vals {
						s := ve.Source()
						switch s.(type) {
						case *ast.Ident:
							foundRef = true
						}
					}
					if foundRef {
						continue
					}
				}

				fmt.Fprintf(w, "%s\t%v\n", val.Path(), val)

			}
			// ensure we flush the output buf
			w.Flush()

			return nil
		})

		if err != nil {
			lg.Fatal().Err(err).Msg("failed to query environment")
		}

	},
}

func init() {
	listCmd.Flags().BoolP("keep-references", "R", false, "Try to eliminate references")

	if err := viper.BindPFlags(listCmd.Flags()); err != nil {
		panic(err)
	}
}
