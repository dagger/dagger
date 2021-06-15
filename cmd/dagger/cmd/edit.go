package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"go.dagger.io/dagger/cmd/dagger/cmd/common"
	"go.dagger.io/dagger/cmd/dagger/logger"
	"go.dagger.io/dagger/state"

	"github.com/google/shlex"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

var editCmd = &cobra.Command{
	Use:   "edit",
	Short: "Interactively edit an environment",
	Args:  cobra.MaximumNArgs(1),
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

		workspace := common.CurrentWorkspace(ctx)
		st := common.CurrentEnvironmentState(ctx, workspace)

		data, err := yaml.Marshal(st)
		if err != nil {
			lg.Fatal().Err(err).Msg("unable to marshal state")
		}

		f, err := os.CreateTemp("", fmt.Sprintf("%s-*.yaml", st.Name))
		if err != nil {
			lg.Fatal().Err(err).Msg("failed to create temporary file")
		}
		tmpPath := f.Name()
		defer os.Remove(tmpPath)

		if _, err := f.Write(data); err != nil {
			lg.Fatal().Err(err).Msg("unable to write file")
		}
		f.Close()

		if err := runEditor(ctx, tmpPath); err != nil {
			lg.Fatal().Err(err).Msg("failed to start editor")
		}

		data, err = os.ReadFile(tmpPath)
		if err != nil {
			lg.Fatal().Err(err).Msg("failed to read temporary file")
		}
		var newState state.State
		if err := yaml.Unmarshal(data, &newState); err != nil {
			lg.Fatal().Err(err).Msg("failed to decode file")
		}
		st.Name = newState.Name
		st.Plan = newState.Plan
		st.Inputs = newState.Inputs
		if err := workspace.Save(ctx, st); err != nil {
			lg.Fatal().Err(err).Msg("failed to save state")
		}
	},
}

func runEditor(ctx context.Context, path string) error {
	editor := os.Getenv("EDITOR")
	var cmd *exec.Cmd
	if editor == "" {
		editor, err := lookupAnyEditor("vim", "nano", "vi")
		if err != nil {
			return err
		}
		cmd = exec.CommandContext(ctx, editor, path)
	} else {
		parts, err := shlex.Split(editor)
		if err != nil {
			return fmt.Errorf("invalid $EDITOR: %s", editor)
		}
		parts = append(parts, path)
		cmd = exec.CommandContext(ctx, parts[0], parts[1:]...) // #nosec
	}

	cmd.Env = os.Environ()
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func lookupAnyEditor(editorNames ...string) (editorPath string, err error) {
	for _, editorName := range editorNames {
		editorPath, err = exec.LookPath(editorName)
		if err == nil {
			return editorPath, nil
		}
	}
	return "", fmt.Errorf("no editor available: dagger attempts to use the editor defined in the EDITOR environment variable, and if that's not set defaults to any of %s, but none of them could be found", strings.Join(editorNames, ", "))
}

func init() {
	if err := viper.BindPFlags(editCmd.Flags()); err != nil {
		panic(err)
	}
}
