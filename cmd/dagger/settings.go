package main

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/client"
	"github.com/juju/ansiterm/tabwriter"
	"github.com/spf13/cobra"
)

var settingsCmd = &cobra.Command{
	Use:     "settings [module] [key] [value]",
	Short:   "Get or set module settings",
	GroupID: workspaceGroup.ID,
	Args:    cobra.MaximumNArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		return withEngine(cmd.Context(), client.Params{}, func(ctx context.Context, engineClient *client.Client) error {
			state, err := loadWorkspaceSettingsState(ctx, engineClient.Dagger())
			if err != nil {
				return err
			}

			switch len(args) {
			case 0:
				return writeWorkspaceSettingsOverview(ctx, cmd.OutOrStdout(), state)
			case 1:
				module, err := state.lookup(args[0])
				if err != nil {
					return err
				}
				return writeWorkspaceModuleSettings(ctx, cmd.OutOrStdout(), state.Workspace, module)
			case 2:
				module, err := state.lookup(args[0])
				if err != nil {
					return err
				}
				arg, err := module.lookupArg(args[1])
				if err != nil {
					return err
				}
				value, ok, err := readWorkspaceModuleSettingValue(ctx, state.Workspace, module.Name, arg.Name)
				if err != nil {
					return err
				}
				if !ok {
					value = ""
				}
				_, err = fmt.Fprintln(cmd.OutOrStdout(), value)
				return err
			case 3:
				module, err := state.lookup(args[0])
				if err != nil {
					return err
				}
				arg, err := module.lookupArg(args[1])
				if err != nil {
					return err
				}
				_, err = state.Workspace.ConfigWrite(ctx, workspaceSettingConfigKey(module.Name, arg.Name), args[2])
				return err
			default:
				return fmt.Errorf("expected 0-3 arguments, got %d", len(args))
			}
		})
	},
}

type workspaceSettingsModule struct {
	Name        string
	Constructor *modFunction
}

func (m workspaceSettingsModule) lookupArg(name string) (*modFunctionArg, error) {
	if m.Constructor == nil {
		return nil, fmt.Errorf("module %q has no discoverable settings", m.Name)
	}
	for _, arg := range m.Constructor.Args {
		switch {
		case strings.EqualFold(arg.Name, name):
			return arg, nil
		case strings.EqualFold(arg.FlagName(), name):
			return arg, nil
		}
	}
	return nil, fmt.Errorf("module %q has no setting %q", m.Name, name)
}

type workspaceSettingsState struct {
	Workspace *dagger.Workspace
	Modules   []workspaceSettingsModule
	byName    map[string]workspaceSettingsModule
}

func loadWorkspaceSettingsState(ctx context.Context, dag *dagger.Client) (*workspaceSettingsState, error) {
	ws := dag.CurrentWorkspace()
	hasConfig, err := ws.HasConfig(ctx)
	if err != nil {
		return nil, err
	}
	if !hasConfig {
		return nil, fmt.Errorf("no config.toml found in workspace")
	}

	installed, err := ws.ModuleList(ctx)
	if err != nil {
		return nil, err
	}

	def, err := initializeWorkspace(ctx, dag, loadTypeDefsOpts{HideCore: true})
	if err != nil {
		return nil, err
	}

	state := &workspaceSettingsState{
		Workspace: ws,
		Modules:   make([]workspaceSettingsModule, 0, len(installed)),
		byName:    make(map[string]workspaceSettingsModule, len(installed)),
	}

	for i := range installed {
		name, err := installed[i].Name(ctx)
		if err != nil {
			return nil, err
		}

		var constructor *modFunction
		if obj := def.GetObject(name); obj != nil {
			constructor = obj.Constructor
		}

		module := workspaceSettingsModule{
			Name:        name,
			Constructor: constructor,
		}
		state.Modules = append(state.Modules, module)
		state.byName[name] = module
	}
	sort.Slice(state.Modules, func(i, j int) bool {
		return state.Modules[i].Name < state.Modules[j].Name
	})

	return state, nil
}

func (s *workspaceSettingsState) lookup(name string) (workspaceSettingsModule, error) {
	if module, ok := s.byName[name]; ok {
		return module, nil
	}
	for alias, module := range s.byName {
		if strings.EqualFold(alias, name) || gqlObjectName(alias) == gqlObjectName(name) {
			return module, nil
		}
	}
	return workspaceSettingsModule{}, fmt.Errorf("module %q is not installed in the workspace", name)
}

func workspaceSettingConfigKey(moduleName, argName string) string {
	return fmt.Sprintf("modules.%s.settings.%s", moduleName, argName)
}

func writeWorkspaceSettingsOverview(ctx context.Context, out io.Writer, state *workspaceSettingsState) error {
	return writeWorkspaceSettingsTable(ctx, out, state.Workspace, state.Modules)
}

func writeWorkspaceModuleSettings(ctx context.Context, out io.Writer, ws *dagger.Workspace, module workspaceSettingsModule) error {
	return writeWorkspaceSettingsTable(ctx, out, ws, []workspaceSettingsModule{module})
}

type workspaceSettingsTableRow struct {
	module      string
	key         string
	value       string
	description string
}

func writeWorkspaceSettingsTable(ctx context.Context, out io.Writer, ws *dagger.Workspace, modules []workspaceSettingsModule) error {
	var rows []workspaceSettingsTableRow
	for _, module := range modules {
		if module.Constructor == nil || len(module.Constructor.Args) == 0 {
			continue
		}
		for _, arg := range module.Constructor.Args {
			value, ok, err := readWorkspaceModuleSettingValue(ctx, ws, module.Name, arg.Name)
			if err != nil {
				return err
			}
			if !ok {
				value = ""
			}
			rows = append(rows, workspaceSettingsTableRow{
				module:      module.Name,
				key:         arg.Name,
				value:       value,
				description: arg.Short(),
			})
		}
	}

	if len(rows) == 0 {
		_, err := fmt.Fprintln(out, "(no settings)")
		return err
	}

	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "MODULE\tKEY\tVALUE\tDESCRIPTION"); err != nil {
		return err
	}
	for _, row := range rows {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", row.module, row.key, row.value, row.description); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func readWorkspaceModuleSettingValue(ctx context.Context, ws *dagger.Workspace, moduleName, argName string) (string, bool, error) {
	value, err := ws.ConfigRead(ctx, dagger.WorkspaceConfigReadOpts{
		Key: workspaceSettingConfigKey(moduleName, argName),
	})
	if err != nil {
		if strings.Contains(err.Error(), "is not set") {
			return "", false, nil
		}
		return "", false, err
	}
	return strings.TrimRight(value, "\n"), true, nil
}
