package cmd

import (
	"os"
	"strings"

	"github.com/docker/buildx/util/logutil"
	"github.com/moby/buildkit/util/appcontext"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.dagger.io/dagger/cmd/dagger/cmd/project"
	"go.dagger.io/dagger/cmd/dagger/logger"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var rootCmd = &cobra.Command{
	Use:   "dagger",
	Short: "A programmable deployment system",
}

func init() {
	// filter out useless commandConn.CloseWrite warning message that can occur
	// when dagger runs for the first time. This should be fixed upstream:
	// unreachable: "commandConn.CloseWrite: commandconn: failed to wait: signal: killed"
	// https://github.com/docker/cli/blob/3fb4fb83dfb5db0c0753a8316f21aea54dab32c5/cli/connhelper/commandconn/commandconn.go#L203-L214
	logrus.AddHook(logutil.NewFilter([]logrus.Level{
		logrus.WarnLevel,
	},
		"commandConn.CloseRead:",
		"commandConn.CloseWrite:",
	))

	rootCmd.PersistentFlags().String("log-format", "auto", "Log format (auto, plain, tty, json)")
	rootCmd.PersistentFlags().StringP("log-level", "l", "info", "Log level")
	rootCmd.PersistentFlags().Bool("experimental", false, "Enable experimental features")

	rootCmd.PersistentPreRun = func(cmd *cobra.Command, _ []string) {
		go checkVersion()
	}
	rootCmd.PersistentPostRun = func(*cobra.Command, []string) {
		warnVersion()
	}

	rootCmd.AddCommand(
		versionCmd,
		docCmd,
		doCmd,
		project.Cmd,
		loginCmd,
		logoutCmd,
		apiTestCmd,
	)

	rootCmd.CompletionOptions.HiddenDefaultCmd = true

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
		span   trace.Span
	)

	if len(os.Args) > 1 {
		spanName := strings.Join(os.Args[1:], " ")
		if overrideName := os.Getenv("DAGGER_TRACE_SPAN_NAME"); overrideName != "" {
			spanName = overrideName
		}
		ctx, span = otel.Tracer("dagger").Start(ctx, spanName)
		// Record the action
		span.AddEvent("command", trace.WithAttributes(
			attribute.String("args", strings.Join(os.Args, " ")),
		))
	}

	defer func() {
		if span != nil {
			span.End()
		}
		closer.Close()
	}()

	if err := rootCmd.ExecuteContext(ctx); err != nil {
		lg.Fatal().Err(err).Msg("failed to execute command")
	}
}
