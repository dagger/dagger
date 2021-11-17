package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"cuelang.org/go/cue"
	"github.com/containerd/containerd/platforms"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"go.dagger.io/dagger/cmd/dagger/cmd/common"
	"go.dagger.io/dagger/cmd/dagger/logger"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/environment"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
	"go.dagger.io/dagger/state"
	"go.mozilla.org/sops/v3"
	"go.mozilla.org/sops/v3/decrypt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var computeCmd = &cobra.Command{
	Use:    "compute CONFIG",
	Short:  "Compute a configuration (DEPRECATED)",
	Args:   cobra.ExactArgs(1),
	Hidden: true,
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

		doneCh := common.TrackCommand(ctx, cmd)

		st := &state.State{
			Context:  plancontext.New(),
			Name:     "FIXME",
			Platform: platforms.Format(specs.Platform{OS: "linux", Architecture: "amd64"}),
			Path:     args[0],
			Plan: state.Plan{
				Module: args[0],
			},
		}

		for _, input := range viper.GetStringSlice("input-string") {
			parts := strings.SplitN(input, "=", 2)
			if len(parts) != 2 {
				lg.Fatal().Msgf("failed to parse input: input-string")
			}

			k, v := parts[0], parts[1]
			err := st.SetInput(k, state.TextInput(v))
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
			if len(parts) != 2 {
				lg.Fatal().Msgf("failed to parse input: input-dir")
			}

			k, v := parts[0], parts[1]
			err := st.SetInput(k, state.DirInput(v, []string{}, []string{}))
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
			if len(parts) != 2 {
				lg.Fatal().Msgf("failed to parse input: input-git")
			}

			k, v := parts[0], parts[1]
			err := st.SetInput(k, state.GitInput(v, "", ""))
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

			err = st.SetInput("", state.JSONInput(string(content)))
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

			err = st.SetInput("", state.YAMLInput(string(content)))
			if err != nil {
				lg.Fatal().Err(err).Msg("failed to add input")
			}
		}

		if f := viper.GetString("input-file"); f != "" {
			lg := lg.With().Str("path", f).Logger()

			parts := strings.SplitN(f, "=", 2)
			k, v := parts[0], parts[1]

			content, err := os.ReadFile(v)
			if err != nil {
				lg.Fatal().Err(err).Msg("failed to read file")
			}

			if len(content) > 0 {
				err = st.SetInput(k, state.FileInput(v))
				if err != nil {
					lg.Fatal().Err(err).Msg("failed to set input string")
				}
			}
		}

		cl := common.NewClient(ctx)

		v := compiler.NewValue()
		plan, err := st.CompilePlan(ctx)
		if err != nil {
			lg.Fatal().Err(err).Msg("failed to compile plan")
		}
		if err := v.FillPath(cue.MakePath(), plan); err != nil {
			lg.Fatal().Err(err).Msg("failed to compile plan")
		}

		inputs, err := st.CompileInputs()
		if err != nil {
			lg.Fatal().Err(err).Msg("failed to compile inputs")
		}

		if err := v.FillPath(cue.MakePath(), inputs); err != nil {
			lg.Fatal().Err(err).Msg("failed to compile inputs")
		}

		env, err := environment.New(st)
		if err != nil {
			lg.Fatal().Msg("unable to create environment")
		}

		err = cl.Do(ctx, env.Context(), func(ctx context.Context, s solver.Solver) error {
			// check that all inputs are set
			checkInputs(ctx, env)

			if err := env.Up(ctx, s); err != nil {
				return err
			}

			if err := v.FillPath(cue.MakePath(), env.Computed()); err != nil {
				return err
			}

			fmt.Println(v.JSON())
			return nil
		})

		<-doneCh

		if err != nil {
			lg.Fatal().Err(err).Msg("failed to up environment")
		}
	},
}

func init() {
	computeCmd.Flags().StringSlice("input-string", []string{}, "TARGET=STRING")
	computeCmd.Flags().StringSlice("input-dir", []string{}, "TARGET=PATH")
	computeCmd.Flags().String("input-file", "", "TARGET=PATH")
	computeCmd.Flags().StringSlice("input-git", []string{}, "TARGET=REMOTE#REF")
	computeCmd.Flags().String("input-json", "", "JSON")
	computeCmd.Flags().String("input-yaml", "", "YAML")

	if err := viper.BindPFlags(computeCmd.Flags()); err != nil {
		panic(err)
	}
}
