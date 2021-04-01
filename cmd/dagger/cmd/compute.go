package cmd

import (
	"encoding/json"
	"errors"
	"os"
	"strings"

	"dagger.io/go/cmd/dagger/cmd/common"
	"dagger.io/go/cmd/dagger/logger"
	"dagger.io/go/dagger"
	"go.mozilla.org/sops/v3"
	"go.mozilla.org/sops/v3/decrypt"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var computeCmd = &cobra.Command{
	Use:   "compute CONFIG",
	Short: "Compute a configuration",
	Args:  cobra.ExactArgs(1),
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

		st := &dagger.DeploymentState{
			ID:         uuid.New().String(),
			Name:       "FIXME",
			PlanSource: dagger.DirInput(args[0], []string{"*.cue", "cue.mod"}),
		}

		for _, input := range viper.GetStringSlice("input-string") {
			parts := strings.SplitN(input, "=", 2)
			k, v := parts[0], parts[1]
			err := st.AddInput(k, dagger.TextInput(v))
			if err != nil {
				lg.
					Fatal().
					Err(err).
					Str("input", k).
					Msg("failed to add input")
			}
		}

		for _, input := range viper.GetStringSlice("input-dir") {
			parts := strings.SplitN(input, "=", 2)
			k, v := parts[0], parts[1]
			err := st.AddInput(k, dagger.DirInput(v, []string{}))
			if err != nil {
				lg.
					Fatal().
					Err(err).
					Str("input", k).
					Msg("failed to add input")
			}
		}

		for _, input := range viper.GetStringSlice("input-git") {
			parts := strings.SplitN(input, "=", 2)
			k, v := parts[0], parts[1]
			err := st.AddInput(k, dagger.GitInput(v, "", ""))
			if err != nil {
				lg.
					Fatal().
					Err(err).
					Str("input", k).
					Msg("failed to add input")
			}
		}

		if f := viper.GetString("input-json"); f != "" {
			lg := lg.With().Str("path", f).Logger()

			content, err := os.ReadFile(f)
			if err != nil {
				lg.Fatal().Err(err).Msg("failed to read file")
			}

			plaintext, err := decrypt.Data(content, "json")
			if err != nil && !errors.Is(err, sops.MetadataNotFound) {
				lg.Fatal().Err(err).Msg("unable to decrypt")
			}

			if len(plaintext) > 0 {
				content = plaintext
			}

			if !json.Valid(content) {
				lg.Fatal().Msg("invalid json")
			}

			err = st.AddInput("", dagger.JSONInput(string(content)))
			if err != nil {
				lg.Fatal().Err(err).Msg("failed to add input")
			}
		}

		if f := viper.GetString("input-yaml"); f != "" {
			lg := lg.With().Str("path", f).Logger()

			content, err := os.ReadFile(f)
			if err != nil {
				lg.Fatal().Err(err).Msg("failed to read file")
			}

			plaintext, err := decrypt.Data(content, "yaml")
			if err != nil && !errors.Is(err, sops.MetadataNotFound) {
				lg.Fatal().Err(err).Msg("unable to decrypt")
			}

			if len(plaintext) > 0 {
				content = plaintext
			}

			err = st.AddInput("", dagger.YAMLInput(string(content)))
			if err != nil {
				lg.Fatal().Err(err).Msg("failed to add input")
			}
		}

		deployment, err := dagger.NewDeployment(st)
		if err != nil {
			lg.Fatal().Err(err).Msg("unable to initialize deployment")
		}

		common.DeploymentUp(ctx, deployment)
	},
}

func init() {
	computeCmd.Flags().StringSlice("input-string", []string{}, "TARGET=STRING")
	computeCmd.Flags().StringSlice("input-dir", []string{}, "TARGET=PATH")
	computeCmd.Flags().StringSlice("input-git", []string{}, "TARGET=REMOTE#REF")
	computeCmd.Flags().String("input-json", "", "JSON")
	computeCmd.Flags().String("input-yaml", "", "YAML")

	if err := viper.BindPFlags(computeCmd.Flags()); err != nil {
		panic(err)
	}
}
