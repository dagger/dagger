package cmd

import (
	"os"
	"strings"

	"dagger.io/go/cmd/dagger/logger"
	"github.com/moby/buildkit/util/appcontext"
	"github.com/opentracing/opentracing-go"
	otlog "github.com/opentracing/opentracing-go/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var rootCmd = &cobra.Command{
	Use:   "dagger",
	Short: "Open-source workflow engine",
}

func init() {
	rootCmd.PersistentFlags().String("log-format", "", "Log format (json, pretty). Defaults to json if the terminal is not a tty")
	rootCmd.PersistentFlags().StringP("log-level", "l", "debug", "Log level")

	rootCmd.AddCommand(
		computeCmd,
		// Create an env
		// Change settings on an env
		// View or edit env serti
		//		settingsCmd,
		// Query the state of an env
		//		getCmd,
		//		unsetCmd,
		//		computeCmd,
		//		listCmd,
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
