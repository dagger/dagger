package cmd

import (
	"strings"

	"dagger.io/go/cmd/dagger/logger"
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
	lg := logger.New()

	if err := rootCmd.Execute(); err != nil {
		lg.Fatal().Err(err).Msg("failed to execute command")
	}
}
