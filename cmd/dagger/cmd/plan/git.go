package plan

import (
	"dagger.io/go/cmd/dagger/logger"
	"dagger.io/go/dagger"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var gitCmd = &cobra.Command{
	Use:   "git REMOTE [REF] [SUBDIR]",
	Short: "Load plan from a git package",
	Args:  cobra.RangeArgs(1, 3),
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

		ref := "HEAD"
		if len(args) > 1 {
			ref = args[1]
		}

		subDir := ""
		if len(args) > 2 {
			subDir = args[2]
		}

		updateEnvironmentPlan(ctx, dagger.GitInput(args[0], ref, subDir))
	},
}

func init() {
	if err := viper.BindPFlags(gitCmd.Flags()); err != nil {
		panic(err)
	}
}
