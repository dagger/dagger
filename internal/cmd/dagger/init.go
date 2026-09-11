package daggercmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"github.com/charmbracelet/huh"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/client"
	"github.com/spf13/cobra"
)

// initState contains presentation data from a successfully exported workspace.
type initState struct {
	ConfigPath string
	Created    bool
}

func runInit(cmd *cobra.Command, _ []string) error {
	interactive := canPromptForInit(progress, stdinIsTTY, autoApply)
	var enableCloud bool
	err := withSetupSessions(cmd.Context(), nil, func(ctx context.Context, connect func(context.Context) (*client.Client, func(), error)) error {
		if err := func() error {
			session, closeSession, err := connect(ctx)
			if err != nil {
				return err
			}
			defer closeSession()
			state, err := initializeWorkspaceConfig(ctx, session.Dagger())
			if err != nil {
				return err
			}
			return printWorkspaceInitialized(cmd, state)
		}(); err != nil {
			return err
		}
		if interactive {
			var err error
			enableCloud, err = offerWorkspaceNextSteps(ctx, cmd, connect)
			if err != nil {
				return fmt.Errorf("workspace configuration is ready; optional setup stopped: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if interactive {
		return runWorkspaceCloudNextStep(cmd, enableCloud)
	}
	return printWorkspaceNextSteps(cmd)
}

func initializeWorkspaceConfig(ctx context.Context, dag *dagger.Client) (*initState, error) {
	current := dag.CurrentWorkspace()
	configFile, err := current.ConfigFile(ctx)
	if err != nil {
		return nil, err
	}
	updated, err := materializeWorkspace(ctx, dag, current.WithInitialized())
	if err != nil {
		return nil, err
	}
	// Export owns local workspace validation and host writes, as it does for
	// module installation and config updates. Do not report success before it.
	if err := updated.Export(ctx); err != nil {
		return nil, err
	}
	configPath, err := workspaceConfigHostPath(ctx, updated)
	if err != nil {
		return nil, err
	}
	return &initState{ConfigPath: configPath, Created: configFile == ""}, nil
}

func printWorkspaceInitialized(cmd *cobra.Command, state *initState) error {
	out := cmd.OutOrStdout()
	verb := "found"
	if state.Created {
		verb = "initialized"
	}
	configPath := state.ConfigPath
	if cwd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(cwd, configPath); err == nil {
			configPath = filepath.ToSlash(rel)
			if filepath.Dir(rel) == "." {
				configPath = "./" + configPath
			}
		}
	}
	_, err := fmt.Fprintf(out, "Workspace configuration %s at %s\n", verb, configPath)
	return err
}

func printWorkspaceNextSteps(cmd *cobra.Command) error {
	prefix := commandPrefixForLocalWorkspace(cmd)
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "\nTo continue setup, run these commands in order:\n\n#!/bin/sh\n\n# 1. Find and install suitable modules\n%s module recommend\n\n# 2. Enable cloud checks\n%s cloud checks on\n", prefix, prefix)
	return err
}

func canPromptForInit(progress string, stdinIsTTY, apply bool) bool {
	return !apply && progress == "tty" && (stdinIsTTY || os.Getenv("DAGGER_TUI_CONSOLE") != "")
}

// offerWorkspaceNextSteps runs inside its caller's frontend. Each operation
// connects separately, so recommendations see files written by migration.
func offerWorkspaceNextSteps(ctx context.Context, cmd *cobra.Command, connect func(context.Context) (*client.Client, func(), error)) (bool, error) {
	prefix := commandPrefixForLocalWorkspace(cmd)
	return offerInitNextSteps(
		func(title, command string) (bool, error) {
			command = prefix + " " + command
			selected := false
			form := huh.NewForm(huh.NewGroup(setupCommandChoice(title, command, &selected)))
			if err := Frontend.HandleForm(ctx, form); err != nil {
				return false, err
			}
			status := "Skipped"
			if selected {
				status = "Selected"
			}
			setupMessage(ctx, status+": "+command, status+" command:\n\n    "+command)
			return selected, nil
		},
		func() error {
			session, closeSession, err := connect(ctx)
			if err != nil {
				return err
			}
			defer closeSession()
			recs, install, err := planRecommend(ctx, session.Dagger())
			if err != nil || !install {
				return err
			}
			return installRecommended(ctx, session.Dagger(), recs)
		},
		func() bool {
			remote, _, err := selectedRemoteWorkspaceAddress(ctx, "cloud checks")
			if err != nil {
				return false
			}
			state, ok, err := loadWorkspaceAutocheckState(ctx, remote)
			return err == nil && ok && state.Enabled
		},
	)
}

func setupCommandChoice(title, command string, selected *bool) *idtui.ExplicitConfirm {
	return idtui.NewExplicitConfirm("Run", "Skip", selected).
		Title(title).
		Description(command)
}

func runWorkspaceCloudNextStep(cmd *cobra.Command, enableCloud bool) error {
	if enableCloud {
		// Authentication can open a browser. Run it after the recommendation
		// frontend has released the terminal, as the standalone command does.
		fmt.Fprintf(cmd.OutOrStdout(), "\nRun:\n%s cloud checks on\n\n", commandPrefixForLocalWorkspace(cmd))
		return runCloudCheckSet(true)(cmd, nil)
	}
	return nil
}

// Optional operations are offered in order. An accepted operation finishes
// before the next offer. Skipping one does not prevent an independent offer.
func offerInitNextSteps(
	prompt func(title, command string) (bool, error),
	recommend func() error,
	cloudChecksEnabled func() bool,
) (bool, error) {
	accepted, err := prompt("Find and install suitable modules?", "module recommend")
	if err != nil {
		return false, err
	}
	if accepted {
		if err := recommend(); err != nil {
			return false, err
		}
	}
	if cloudChecksEnabled() {
		return false, nil
	}
	return prompt("Enable cloud checks?", "cloud checks on")
}

func shellQuote(value string) string {
	if value != "" && !strings.ContainsAny(value, " \t\r\n'\"`$;&|<>(){}[]*?!\\#~") {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
