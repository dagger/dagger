package input

import (
	"context"
	"fmt"

	"dagger.io/go/cmd/dagger/cmd/common"
	"dagger.io/go/cmd/dagger/logger"
	"dagger.io/go/dagger"

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


			for _, i := range inputs {
				// inst, ipath := i.Reference()
				//l, _ := i.Label()
				////fmt.Printf("%v: %v\n", l, i)
				//fmt.Printf("%v: %v\n", ipath, inst)

				inst, _ := i.Reference()
				pkg := ""
				if inst != nil {
					pkg = fmt.Sprintf("(from %s)", inst.ImportPath)
				}
				fmt.Printf("%s: %v  %s %v\n", i.Path(), i, pkg, i.IsConcrete())
			}

			return nil
		})

		if err != nil {
			lg.Fatal().Err(err).Msg("failed to query deployment")
		}

	},
}

