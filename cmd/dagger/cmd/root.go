package cmd

import (
	"os"
	"strings"

	inputCmd "dagger.io/go/cmd/dagger/cmd/input"
	"dagger.io/go/cmd/dagger/cmd/output"
	"dagger.io/go/cmd/dagger/cmd/plan"
	"dagger.io/go/cmd/dagger/logger"
	"github.com/moby/buildkit/util/appcontext"
	"github.com/opentracing/opentracing-go"
	otlog "github.com/opentracing/opentracing-go/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var rootCmd = &cobra.Command{
	Use:   "dagger",
	Short: "A system for application delivery as code (ADC)",
}

func init() {
	rootCmd.PersistentFlags().String("log-format", "", "Log format (json, pretty). Defaults to json if the terminal is not a tty")
	rootCmd.PersistentFlags().StringP("log-level", "l", "debug", "Log level")
	rootCmd.PersistentFlags().StringP("deployment", "d", "", "Select a deployment")

	rootCmd.AddCommand(
		computeCmd,
		newCmd,
		listCmd,
		queryCmd,
		upCmd,
		downCmd,
		deleteCmd,
		historyCmd,
		loginCmd,
		logoutCmd,
		plan.Cmd,
		inputCmd.Cmd,
		output.Cmd,
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
