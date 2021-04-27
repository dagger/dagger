package cmd

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"path"
	"strings"
	"text/tabwriter"

	"dagger.io/go/cmd/dagger/logger"
	"dagger.io/go/dagger"
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
		store, err := dagger.DefaultStore()
		if err != nil {
			lg.Fatal().Err(err).Msg("failed to load store")
		}

		environments, err := store.ListEnvironments(ctx)
		if err != nil {
			lg.
				Fatal().
				Err(err).
				Msg("cannot list environments")
		}

		environmentID := getCurrentEnvironmentID(ctx, store)
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 1, ' ', tabwriter.TabIndent)
		for _, r := range environments {
			line := fmt.Sprintf("%s\t%s\t", r.Name, formatPlanSource(r.PlanSource))
			if r.ID == environmentID {
				line = fmt.Sprintf("%s- active environment", line)
			}
			fmt.Fprintln(w, line)
		}
		w.Flush()
	},
}

func init() {
	if err := viper.BindPFlags(listCmd.Flags()); err != nil {
		panic(err)
	}
}

func getCurrentEnvironmentID(ctx context.Context, store *dagger.Store) string {
	lg := log.Ctx(ctx)

	wd, err := os.Getwd()
	if err != nil {
		lg.Warn().Err(err).Msg("cannot get current working directory")
		return ""
	}

	st, err := store.LookupEnvironmentByPath(ctx, wd)
	if err != nil {
		// Ignore error
		return ""
	}

	if len(st) == 1 {
		return st[0].ID
	}

	return ""
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

func formatPlanSource(i dagger.Input) string {
	switch i.Type {
	case dagger.InputTypeDir:
		return formatPath(i.Dir.Path)
	case dagger.InputTypeGit:
		return i.Git.Remote
	case dagger.InputTypeDocker:
		return i.Docker.Ref
	}

	return "no plan"
}
