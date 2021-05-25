package input

import (
	"path/filepath"
	"strings"

	"dagger.io/go/cmd/dagger/cmd/common"
	"dagger.io/go/cmd/dagger/logger"
	"dagger.io/go/dagger/state"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var dirCmd = &cobra.Command{
	Use:   "dir TARGET PATH",
	Short: "Add a local directory as input artifact",
	Args:  cobra.ExactArgs(2),
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

		p, err := filepath.Abs(args[1])
		if err != nil {
			lg.Fatal().Err(err).Str("path", args[1]).Msg("unable to resolve path")
		}

		workspace := common.CurrentWorkspace(ctx)
		if !strings.HasPrefix(p, workspace.Path) {
			lg.Fatal().Err(err).Str("path", args[1]).Msg("dir is outside the workspace")
		}
		p, err = filepath.Rel(workspace.Path, p)
		if err != nil {
			lg.Fatal().Err(err).Str("path", args[1]).Msg("unable to resolve path")
		}
		if !strings.HasPrefix(p, ".") {
			p = "./" + p
		}

		updateEnvironmentInput(ctx, args[0], state.DirInput(p, []string{}))
	},
}

func init() {
	if err := viper.BindPFlags(dirCmd.Flags()); err != nil {
		panic(err)
	}
}
