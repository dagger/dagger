package cmd

import (
	"os"
	"strings"

	"github.com/moby/buildkit/util/appcontext"
	"github.com/opentracing/opentracing-go"
	otlog "github.com/opentracing/opentracing-go/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.dagger.io/dagger/cmd/dagger/cmd/input"
	"go.dagger.io/dagger/cmd/dagger/cmd/output"
	"go.dagger.io/dagger/cmd/dagger/logger"
)

var rootCmd = &cobra.Command{
	Use:   "dagger",
	Short: "A programmable deployment system",
}

func init() {
	rootCmd.PersistentFlags().String("log-format", "", "Log format (json, pretty). Defaults to json if the terminal is not a tty")
	rootCmd.PersistentFlags().StringP("log-level", "l", "info", "Log level")
	rootCmd.PersistentFlags().StringP("environment", "e", "", "Select an environment")
	rootCmd.PersistentFlags().StringP("workspace", "w", "", "Specify a workspace (defaults to current git repository)")

	rootCmd.PersistentPreRun = func(*cobra.Command, []string) {
		go checkVersion()
	}
	rootCmd.PersistentPostRun = func(*cobra.Command, []string) {
		warnVersion()
	}

	rootCmd.AddCommand(
		initCmd,
		newCmd,
		computeCmd,
		listCmd,
		queryCmd,
		upCmd,
		downCmd,
		editCmd,
		historyCmd,
		loginCmd,
		logoutCmd,
		input.Cmd,
		output.Cmd,
		versionCmd,
		docCmd,
	)

	if err := viper.BindPFlags(rootCmd.PersistentFlags()); err != nil {
		panic(err)
	}
	viper.SetEnvPrefix("dagger")
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	viper.AutomaticEnv()
}

func Execute() {
	var (
		ctx = appcontext.Context()
		// `--log-*` flags have not been parsed yet at this point so we get a
		// default logger. Therefore, we can't store the logger into the context.
		lg     = logger.New()
		closer = logger.InitTracing()
		span   opentracing.Span
	)

	if len(os.Args) > 1 {
		span, ctx = opentracing.StartSpanFromContext(ctx, os.Args[1])
		span.LogFields(otlog.String("command", strings.Join(os.Args, " ")))
	}

	defer func() {
		if span != nil {
			span.Finish()
		}
		closer.Close()
	}()

	if err := rootCmd.ExecuteContext(ctx); err != nil {
		lg.Fatal().Err(err).Msg("failed to execute command")
	}
}
