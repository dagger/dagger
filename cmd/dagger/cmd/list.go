package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path"
	"strings"
	"text/tabwriter"

	"dagger.io/go/cmd/dagger/logger"
	"dagger.io/go/dagger/state"
	"github.com/rs/zerolog/log"
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

		var (
			workspace = viper.GetString("workspace")
			err       error
		)
		if workspace == "" {
			workspace, err = state.CurrentWorkspace(ctx)
			if err != nil {
				lg.
					Fatal().
					Err(err).
					Msg("failed to determine current workspace")
			}
		}

		environments, err := state.List(ctx, workspace)
		if err != nil {
			lg.
				Fatal().
				Err(err).
				Msg("cannot list environments")
		}

		environmentPath := getCurrentEnvironmentPath(ctx)
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 1, ' ', tabwriter.TabIndent)
		defer w.Flush()
		for _, e := range environments {
			line := fmt.Sprintf("%s\t%s\t", e.Name, formatPath(e.Path))
			if e.Path == environmentPath {
				line = fmt.Sprintf("%s- active environment", line)
			}
			fmt.Fprintln(w, line)
		}
	},
}

func getCurrentEnvironmentPath(ctx context.Context) string {
	lg := log.Ctx(ctx)

	st, err := state.Current(ctx)
	if err != nil {
		// Ignore error if not initialized
		if errors.Is(err, state.ErrNotInit) {
			return ""
		}
		lg.Fatal().Err(err).Msg("failed to load current environment")
	}

	return st.Path
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
