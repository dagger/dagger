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
	Short: "List for the inputs of a deployment",
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

		deployment := common.GetCurrentDeploymentState(ctx, store)

		// print any persisted inputs
		if len(deployment.Inputs) > 0 {
			fmt.Println("Saved Inputs:")
			for _, input := range deployment.Inputs {
				// Todo, how to pull apart an input to print relevant information
				fmt.Printf("%s: %v\n", input.Key, input.Value)
			}
			// add some space
			fmt.Println()
		}

		lg = lg.With().
			Str("deploymentName", deployment.Name).
			Str("deploymentId", deployment.ID).
			Logger()

		c, err := dagger.NewClient(ctx, "", false)
		if err != nil {
			lg.Fatal().Err(err).Msg("unable to create client")
		}

		_, err = c.Do(ctx, deployment, func(lCtx context.Context, lDeploy *dagger.Deployment, lSolver dagger.Solver) error {
			inputs, err := lDeploy.ScanInputs()
			if err != nil {
				return err
			}

			fmt.Println("Plan Inputs:")
			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "Path\tFrom\tType")

			for _, val := range inputs {
				fmt.Println("======================")
				inst, path := val.Reference()
				fmt.Println(val, inst, path)
				//fmt.Printf("%#+v\n", val.Source())

				// check for references
				// this is here because it has issues
				// so we wrap it in a flag to control its usage while debugging
				_, vals := val.Expr()
				for _, ve := range vals {
					inst, path := ve.Reference()
					fmt.Println(ve, inst, path)
					//s := ve.Source()
					//fmt.Printf("%d: %#+v\n", i, s)
				}
				if !viper.GetBool("keep-references") {
					foundRef := false
					for _, ve := range vals {
						// fmt.Println(ve)
						s := ve.Source()
						// fmt.Printf("%#+v\n", s)

						// how do we determine references? (i.e. imports look the same, re: dagger.#Secret)
						switch s.(type) {
						case *ast.Ident:
							foundRef = true
						}
					}
					if foundRef {
						continue
					}
				}

				fmt.Println("======================")
				fmt.Println()

				// Construct output as a tab-table
				// get path / pkg import (if available)
				// inst, _ := val.Reference()
				pkg := "(plan)"
				if inst != nil {
					pkg = inst.ImportPath
				}

				fmt.Fprintf(w, "%s\t%s\t%v\n", val.Path(), pkg, val)

			}
			// ensure we flush the output buf
			w.Flush()

			return nil
		})

		if err != nil {
			lg.Fatal().Err(err).Msg("failed to query deployment")
		}

	},
}

func init() {
	listCmd.Flags().BoolP("keep-references", "R", false, "Try to eliminate references")

	if err := viper.BindPFlags(listCmd.Flags()); err != nil {
		panic(err)
	}
}
