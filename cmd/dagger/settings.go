package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/client"
	"github.com/juju/ansiterm/tabwriter"
	"github.com/spf13/cobra"
)

const workspaceSettingsQuery = `
query WorkspaceSettings($module: String!) {
  currentWorkspace {
    moduleList(module: $module) {
      name
      settings {
        key
        value
        description
      }
    }
  }
}
`

var settingsCmd = &cobra.Command{
	Use:     "settings [module] [key] [value]",
	Short:   "Get or set module settings",
	GroupID: workspaceGroup.ID,
	Args:    cobra.MaximumNArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		return withEngine(cmd.Context(), client.Params{}, func(ctx context.Context, engineClient *client.Client) error {
			moduleName := ""
			if len(args) > 0 {
				moduleName = args[0]
			}

			state, err := loadWorkspaceSettingsState(ctx, engineClient.Dagger(), moduleName)
			if err != nil {
				return err
			}

			switch len(args) {
			case 0, 1:
				return writeWorkspaceSettingsTable(cmd.OutOrStdout(), state.Settings)
			case 2:
				setting, err := state.lookupSetting(args[1])
				if err != nil {
					return err
				}
				_, err = fmt.Fprintln(cmd.OutOrStdout(), setting.Value)
				return err
			case 3:
				setting, err := state.lookupSetting(args[1])
				if err != nil {
					return err
				}
				_, err = state.Workspace.ConfigWrite(ctx, workspaceSettingConfigKey(setting.Module, setting.Key), args[2], dagger.WorkspaceConfigWriteOpts{Here: workspaceHere})
				return err
			default:
				return fmt.Errorf("expected 0-3 arguments, got %d", len(args))
			}
		})
	},
}

func init() {
	addWorkspaceHereFlag(settingsCmd)
}

type workspaceSetting struct {
	Module      string
	Key         string
	Value       string
	Description string
}

type workspaceSettingsState struct {
	Workspace *dagger.Workspace
	Module    string
	Settings  []workspaceSetting
}

func loadWorkspaceSettingsState(ctx context.Context, dag *dagger.Client, moduleName string) (*workspaceSettingsState, error) {
	var res struct {
		CurrentWorkspace struct {
			ModuleList []struct {
				Name     string
				Settings []workspaceSetting
			}
		}
	}
	if err := dag.Do(ctx, &dagger.Request{
		Query:     workspaceSettingsQuery,
		Variables: map[string]any{"module": moduleName},
	}, &dagger.Response{
		Data: &res,
	}); err != nil {
		return nil, err
	}

	settings := make([]workspaceSetting, 0)
	for _, module := range res.CurrentWorkspace.ModuleList {
		for _, setting := range module.Settings {
			setting.Module = module.Name
			settings = append(settings, setting)
		}
	}

	return &workspaceSettingsState{
		Workspace: dag.CurrentWorkspace(),
		Module:    moduleName,
		Settings:  settings,
	}, nil
}

func (s *workspaceSettingsState) lookupSetting(name string) (workspaceSetting, error) {
	if len(s.Settings) == 0 {
		return workspaceSetting{}, fmt.Errorf("module %q has no discoverable settings", s.Module)
	}
	for _, setting := range s.Settings {
		switch {
		case strings.EqualFold(setting.Key, name):
			return setting, nil
		case strings.EqualFold(cliName(setting.Key), name):
			return setting, nil
		}
	}
	return workspaceSetting{}, fmt.Errorf("module %q has no setting %q", s.Module, name)
}

func workspaceSettingConfigKey(moduleName, settingName string) string {
	return fmt.Sprintf("modules.%s.settings.%s", moduleName, settingName)
}

func writeWorkspaceSettingsTable(out io.Writer, settings []workspaceSetting) error {
	if len(settings) == 0 {
		_, err := fmt.Fprintln(out, "(no settings)")
		return err
	}

	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "MODULE\tKEY\tVALUE\tDESCRIPTION"); err != nil {
		return err
	}
	for _, setting := range settings {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
			setting.Module,
			setting.Key,
			setting.Value,
			workspaceSettingShortDescription(setting.Description),
		); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func workspaceSettingShortDescription(description string) string {
	return strings.SplitN(description, "\n", 2)[0]
}
