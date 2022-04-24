package cmd

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.dagger.io/dagger/cmd/dagger/logger"
	"go.dagger.io/dagger/pkg"

	"github.com/octohelm/cuemod/pkg/cuemod"
)

func init() {
	rootCmd.AddCommand(getCmd)

	getCmd.Flags().
		BoolP("update", "u", false, "Update to latest")
	getCmd.Flags().
		StringP("import", "i", "", "declare language for generate. support values: go")

	if err := viper.BindPFlags(getCmd.Flags()); err != nil {
		panic(err)
	}
}

var getCmd = &cobra.Command{
	Use:    "get",
	Short:  "Log into your Dagger account",
	Hidden: true,
	Args:   cobra.MinimumNArgs(1),
	PreRun: func(cmd *cobra.Command, args []string) {
		// Fix Viper bug for duplicate flags:
		// https://github.com/spf13/viper/issues/233
		if err := viper.BindPFlags(cmd.Flags()); err != nil {
			panic(err)
		}
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		lg := logger.New()
		ctx := lg.WithContext(cmd.Context())
		cueModPath, cueModExists := pkg.GetCueModParent()
		if !cueModExists {
			lg.Fatal().Msg("dagger project not found. Run `dagger project init`")
		}

		c := cuemod.ContextFor(cueModPath)

		for i := range args {
			p := args[i]
			err := c.Get(
				cuemod.WithOpts(ctx,
					cuemod.OptUpgrade(viper.GetBool("update")),
					cuemod.OptImport(viper.GetString("import")),
					cuemod.OptVerbose(viper.GetString("log-level") == "debug"),
				), p,
			)
			if err != nil {
				return err
			}
		}
		return nil
	},
}
