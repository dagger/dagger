package input

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"dagger.io/go/cmd/dagger/cmd/common"
	"dagger.io/go/cmd/dagger/logger"
	"dagger.io/go/dagger"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var scanCmd = &cobra.Command{
	Use:   "scan [TARGET] [flags]",
	Short: "Scan for the inputs of a deployment",
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
		fmt.Println("Current Inputs", deployment.Inputs)

		lg = lg.With().
			Str("deploymentName", deployment.Name).
			Str("deploymentId", deployment.ID).
			Logger()

		c, err := dagger.NewClient(ctx, "")
		if err != nil {
			lg.Fatal().Err(err).Msg("unable to create client")
		}

		_, err = c.Do(ctx, deployment, func(lCtx context.Context, lDeploy *dagger.Deployment, lSolver dagger.Solver) error {
			inputs, err := lDeploy.ScanInputs()
			if err != nil {
				return err
			}

			// fmt.Println("\n\nDiscovered Inputs:\n===========================")
			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "Path\tFrom\tRequired\tReference\tVals\tType\t")

			for _, val := range inputs {
				required := true

				// check for optional
				src := val.Source()
				switch src.(type) {
				case *ast.Field:
					f := src.(*ast.Field)
					if f.Optional.File() != nil {
						required = false
					}
				}

				// check for references
				ident := ""
				op, vals := val.Expr()
				if op == cue.AndOp {
					for _, ve := range vals {
						if _, has := ve.Label(); has {
							// foundIdent = true
							ident = fmt.Sprintf("%t", ve)
							break
						}
					}
					//if foundIdent {
						//continue
					//}
				}


				// get path / pkg import (if available)
				inst, _ := val.Reference()
				pkg := "(plan)"
				if inst != nil {
					pkg = fmt.Sprintf("%s", inst.ImportPath)
				}

				fmt.Fprintf(w, "%s\t%s\t%v\t%v\t%v\t%v\t\n", val.Path(), pkg, required, ident, vals, val)

			}
			w.Flush()

			return nil
		})

		if err != nil {
			lg.Fatal().Err(err).Msg("failed to query deployment")
		}

	},
}

