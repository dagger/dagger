package cmd

import (
	"fmt"
	"path/filepath"
	"strings"

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

		workspace := common.CurrentWorkspace(ctx)

		if viper.GetString("environment") != "" {
			lg.
				Fatal().
				Msg("cannot use option -e,--environment for this command")
		}
		name := args[0]

		module := viper.GetString("module")
		if module != "" {
			p, err := filepath.Abs(module)
			if err != nil {
				lg.Fatal().Err(err).Str("path", module).Msg("unable to resolve path")
			}

			if !strings.HasPrefix(p, workspace.Path) {
				lg.Fatal().Err(err).Str("path", module).Msg("module is outside the workspace")
			}
			p, err = filepath.Rel(workspace.Path, p)
			if err != nil {
				lg.Fatal().Err(err).Str("path", module).Msg("unable to resolve path")
			}
			if !strings.HasPrefix(p, ".") {
				p = "./" + p
			}
			module = p
		}

		ws, err := workspace.Create(ctx, name, state.CreateOpts{
			Module:  module,
			Package: viper.GetString("package"),
		})
		if err != nil {
			lg.Fatal().Err(err).Msg("failed to create environment")
		}

		lg.Info().Str("name", name).Msg("created new empty environment")
		lg.Info().Str("name", name).Msg(fmt.Sprintf("to add code to the plan, copy or create cue files under: %s", ws.Plan.Module))
	},
}

func init() {
	newCmd.Flags().StringP("module", "m", "", "references the local path of the cue module to use as a plan, relative to the workspace root")
	newCmd.Flags().StringP("package", "p", "", "references the name of the Cue package within the module to use as a plan. Default: defer to cue loader")
	if err := viper.BindPFlags(newCmd.Flags()); err != nil {
		panic(err)
	}
}
