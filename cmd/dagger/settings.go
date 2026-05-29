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

var workspaceSettingsCmd = newSettingsCmd(false)
var settingsCmd = newSettingsCmd(true)
var settingsGlobal bool

func init() {
	workspaceCmd.AddCommand(workspaceSettingsCmd)
	addWorkspaceHereFlag(workspaceSettingsCmd)
	addWorkspaceHereFlag(settingsCmd)
	workspaceSettingsCmd.Flags().BoolVarP(&settingsGlobal, "global", "g", false, "Write to user-level Dagger config")
	settingsCmd.Flags().BoolVarP(&settingsGlobal, "global", "g", false, "Write to user-level Dagger config")
}

func newSettingsCmd(hidden bool) *cobra.Command {
	return &cobra.Command{
		Use:    "settings [module] [key] [value]",
		Short:  "Get or set module settings",
		Hidden: hidden,
		Args:   cobra.MaximumNArgs(3),
		RunE:   runWorkspaceSettings,
	}
}

func runWorkspaceSettings(cmd *cobra.Command, args []string) error {
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
			return writeWorkspaceSetting(ctx, engineClient.Dagger(), state.Workspace, setting, args[2])
		default:
			return fmt.Errorf("expected 0-3 arguments, got %d", len(args))
		}
	})
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

func writeWorkspaceSetting(ctx context.Context, dag *dagger.Client, ws *dagger.Workspace, setting workspaceSetting, value string) error {
	key := workspaceSettingConfigKey(setting.Module, setting.Key)
	if !settingsGlobal {
		_, err := ws.ConfigWrite(ctx, key, value, dagger.WorkspaceConfigWriteOpts{Here: workspaceHere})
		return err
	}
	var res struct {
		CurrentWorkspace struct {
			ConfigWrite string
		}
	}
	return dag.Do(ctx, &dagger.Request{
		Query: `query($key: String!, $value: String!, $here: Boolean!, $global: Boolean!) {
			currentWorkspace {
				configWrite(key: $key, value: $value, here: $here, global: $global)
			}
		}`,
		Variables: map[string]any{
			"key":    key,
			"value":  value,
			"here":   workspaceHere,
			"global": true,
		},
	}, &dagger.Response{
		Data: &res,
	})
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
