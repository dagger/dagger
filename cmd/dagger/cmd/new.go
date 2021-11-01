package cmd

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.dagger.io/dagger/cmd/dagger/cmd/common"
	"go.dagger.io/dagger/cmd/dagger/logger"
	"go.dagger.io/dagger/state"
)

var newCmd = &cobra.Command{
	Use:   "new <NAME>",
	Short: "Create a new empty environment",
	Args:  cobra.ExactArgs(1),
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

		if viper.GetString("environment") != "" {
			lg.
				Fatal().
				Msg("cannot use option -e,--environment for this command")
		}
		name := args[0]

		st, err := project.Create(ctx, name, state.Plan{
			Package: viper.GetString("package"),
		}, viper.GetString("architecture"))

		if err != nil {
			lg.Fatal().Err(err).Msg("failed to create environment")
		}

		<-common.TrackProjectCommand(ctx, cmd, project, st)
	},
}

func init() {
	newCmd.Flags().StringP("package", "p", "", "references the name of the Cue package within the module to use as a plan. Default: defer to cue loader")
	newCmd.Flags().StringP("architecture", "a", "", "architecture of the running pipeline. Default: host architecture")
	if err := viper.BindPFlags(newCmd.Flags()); err != nil {
		panic(err)
	}
}
