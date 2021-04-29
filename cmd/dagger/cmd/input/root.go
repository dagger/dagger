package input

import (
	"context"
	"io"
	"os"

	"dagger.io/go/cmd/dagger/cmd/common"
	"dagger.io/go/dagger"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// Cmd exposes the top-level command
var Cmd = &cobra.Command{
	Use:   "input",
	Short: "Manage an environment's inputs",
}

func init() {
	Cmd.AddCommand(
		dirCmd,
		gitCmd,
		containerCmd,
		secretCmd,
		textCmd,
		jsonCmd,
		yamlCmd,
		listCmd,
		unsetCmd,
	)
}

func updateEnvironmentInput(ctx context.Context, target string, input dagger.Input) {
	lg := log.Ctx(ctx)

	store, err := dagger.DefaultStore()
	if err != nil {
		lg.Fatal().Err(err).Msg("failed to load store")
	}

	st := common.GetCurrentEnvironmentState(ctx, store)
	st.SetInput(target, input)

	if err := store.UpdateEnvironment(ctx, st, nil); err != nil {
		lg.Fatal().Err(err).Str("environmentId", st.ID).Str("environmentName", st.Name).Msg("cannot update environment")
	}
	lg.Info().Str("environmentId", st.ID).Str("environmentName", st.Name).Msg("updated environment")
}

func readInput(ctx context.Context, source string) string {
	lg := log.Ctx(ctx)

	if !viper.GetBool("file") {
		return source
	}

	if source == "-" {
		// stdin source
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			lg.
				Fatal().
				Err(err).
				Msg("failed to read input from stdin")
		}
		return string(data)
	}

	// file source
	data, err := os.ReadFile(source)
	if err != nil {
		lg.
			Fatal().
			Err(err).
			Str("path", source).
			Msg("failed to read input from file")
	}

	return string(data)
}
