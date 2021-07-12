package input

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.dagger.io/dagger/cmd/dagger/cmd/common"
	"go.dagger.io/dagger/cmd/dagger/logger"
	"go.dagger.io/dagger/state"
)

var gitCmd = &cobra.Command{
	Use:   "git TARGET REMOTE [REF] [SUBDIR]",
	Short: "Add a git repository as input artifact",
	Args:  cobra.RangeArgs(2, 4),
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
		if len(args) > 2 {
			ref = args[2]
		}

		subDir := ""
		if len(args) > 3 {
			subDir = args[3]
		}

		updateEnvironmentInput(ctx, common.NewClient(ctx), args[0], state.GitInput(args[1], ref, subDir))
	},
}

func init() {
	if err := viper.BindPFlags(gitCmd.Flags()); err != nil {
		panic(err)
	}
}
