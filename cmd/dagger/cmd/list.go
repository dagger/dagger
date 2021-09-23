package cmd

import (
	"fmt"
	"os"
	"path"
	"strings"
	"text/tabwriter"

	"github.com/mitchellh/go-homedir"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.dagger.io/dagger/cmd/dagger/cmd/common"
	"go.dagger.io/dagger/cmd/dagger/logger"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List available environments",
	Args:  cobra.NoArgs,
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
		doneCh := common.TrackProjectCommand(ctx, cmd, project, nil)

		environments, err := project.List(ctx)
		if err != nil {
			lg.
				Fatal().
				Err(err).
				Msg("cannot list environments")
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 1, ' ', tabwriter.TabIndent)
		defer w.Flush()
		for _, e := range environments {
			line := fmt.Sprintf("%s\t%s\t", e.Name, formatPath(e.Path))
			fmt.Fprintln(w, line)
		}

		<-doneCh
	},
}

func formatPath(p string) string {
	dir, err := homedir.Dir()
	if err != nil {
		// Ignore error
		return p
	}

	if strings.HasPrefix(p, dir) {
		return path.Join("~", p[len(dir):])
	}
	return p
}

func init() {
	if err := viper.BindPFlags(listCmd.Flags()); err != nil {
		panic(err)
	}
}
