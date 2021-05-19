package cmd

import (
	"fmt"
	"os"
	"os/user"
	"path"
	"strings"
	"text/tabwriter"

	"dagger.io/go/cmd/dagger/cmd/common"
	"dagger.io/go/cmd/dagger/logger"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
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

		workspace := common.CurrentWorkspace(ctx)
		environments, err := workspace.List(ctx)
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
	},
}

func formatPath(p string) string {
	usr, err := user.Current()
	if err != nil {
		// Ignore error
		return p
	}

	dir := usr.HomeDir

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
