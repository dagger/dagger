package input

import (
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.dagger.io/dagger/cmd/dagger/cmd/common"
	"go.dagger.io/dagger/cmd/dagger/logger"
	"go.dagger.io/dagger/state"
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

		updateEnvironmentInput(ctx, common.NewClient(ctx, false), args[0],
			state.DirInput(
				p,
				viper.GetStringSlice("include"),
				viper.GetStringSlice("exclude"),
			),
		)
	},
}

func init() {
	dirCmd.Flags().StringSlice("include", []string{}, "Include pattern")
	dirCmd.Flags().StringSlice("exclude", []string{}, "Exclude pattern")

	if err := viper.BindPFlags(dirCmd.Flags()); err != nil {
		panic(err)
	}
}
