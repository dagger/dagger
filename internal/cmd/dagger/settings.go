package daggercmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"dagger.io/dagger"
	workspacepkg "github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/engine/client"
	"github.com/juju/ansiterm/tabwriter"
	"github.com/spf13/cobra"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

const workspaceSettingsQuery = `
query WorkspaceSettings {
  currentWorkspace {
    modules {
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

const workspaceModuleSettingsQuery = `
query WorkspaceModuleSettings($module: String!) {
  currentWorkspace {
    module(name: $module) {
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

// workspaceModuleSettingsQueryWithIsList additionally requests isList, which
// older engines don't expose. Only multi-value writes need it, so other flows
// use the queries above and keep working against those engines.
const workspaceModuleSettingsQueryWithIsList = `
query WorkspaceModuleSettings($module: String!) {
  currentWorkspace {
    module(name: $module) {
      name
      settings {
        key
        value
        description
        isList
      }
    }
  }
}
`

var settingsCmd = newSettingsCmd(false)
var settingsAliasCmd = newSettingsCmd(true)

func init() {
	addWorkspaceHereFlag(settingsCmd)
	addWorkspaceHereFlag(settingsAliasCmd)
}

var (
	workspaceSettingsUnset  bool
	workspaceSettingsGlobal bool
)

func newSettingsCmd(hidden bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:    "settings [module] [key] [value...]",
		Short:  "Get, set, or unset module settings (use --env for an env overlay)",
		Hidden: hidden,
		Args:   cobra.ArbitraryArgs,
		RunE:   runWorkspaceSettings,
	}
	cmd.Flags().BoolVarP(&workspaceSettingsUnset, "unset", "u", false, "Remove the setting from workspace config")
	cmd.Flags().BoolVarP(&workspaceSettingsGlobal, "global", "g", false, "Store the setting in user-level config instead of the repository, keyed by the workspace's git remote")
	return cmd
}

func runWorkspaceSettings(cmd *cobra.Command, args []string) error {
	if workspaceSettingsUnset && len(args) != 2 {
		return fmt.Errorf("--unset requires MODULE and KEY arguments")
	}
	if workspaceSettingsGlobal && !workspaceSettingsUnset && len(args) < 3 {
		return fmt.Errorf("--global stores a setting in user-level config; pass MODULE KEY VALUE to set or use --unset (reads always show the effective value)")
	}
	envWrite := len(args) >= 3 && !workspaceSettingsUnset && workspaceEnv != ""
	err := runWorkspaceSettingsSession(cmd, args, envWrite, false)
	if envWrite && isUndefinedEnvError(err, workspaceEnv) {
		// A write is the gesture that creates a missing env. The first attempt
		// applies the overlay so existing envs keep full discovery (including
		// modules the env itself adds); only when the env turns out not to
		// exist retry without it, addressing the env explicitly in the config
		// key instead.
		return runWorkspaceSettingsSession(cmd, args, envWrite, true)
	}
	return err
}

// isUndefinedEnvError reports whether err is the engine rejecting the named
// env as undefined. The engine marks the error with GraphQL extensions
// (workspace.UndefinedEnvError), so match those structurally when present;
// fall back to the message prefix for errors that cross boundaries without
// extensions (version-skewed engines, session-connect failures).
func isUndefinedEnvError(err error, env string) bool {
	if err == nil {
		return false
	}
	var gqlErr *gqlerror.Error
	if errors.As(err, &gqlErr) && gqlErr.Extensions["_type"] == workspacepkg.UndefinedEnvErrorType {
		name, _ := gqlErr.Extensions["env"].(string)
		return name == env
	}
	return strings.Contains(err.Error(), fmt.Sprintf(workspacepkg.UndefinedEnvErrorPrefix, env))
}

func runWorkspaceSettingsSession(cmd *cobra.Command, args []string, envWrite, suppressEnv bool) error {
	params := client.Params{}
	if suppressEnv {
		noEnv := ""
		params.WorkspaceEnv = &noEnv
	}
	return withEngine(cmd.Context(), params, func(ctx context.Context, engineClient *client.Client) error {
		moduleName := ""
		if len(args) > 0 {
			moduleName = args[0]
		}

		state, err := loadWorkspaceSettingsState(ctx, engineClient.Dagger(), moduleName, len(args) > 3)
		if err != nil {
			return err
		}

		if workspaceSettingsUnset {
			setting, err := state.lookupSetting(args[1])
			if err != nil {
				return err
			}
			if workspaceSettingsGlobal {
				return unsetUserConfigValue(ctx, userScopedConfigKey(workspaceSettingConfigKey(setting.Module, setting.Key)))
			}
			return state.Workspace.
				WithoutConfigValue(workspaceSettingConfigKey(setting.Module, setting.Key), dagger.WorkspaceWithoutConfigValueOpts{Here: workspaceHere}).
				Export(ctx)
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
		default:
			setting, err := state.lookupSetting(args[1])
			if err != nil {
				return err
			}
			value, values, err := workspaceSettingWriteValue(setting, args[2:])
			if err != nil {
				return err
			}
			if workspaceSettingsGlobal {
				// User-level writes happen client-side; --env composes there
				// through userScopedConfigKey, and a personal env comes into
				// being by the write itself, so none of the env staging below
				// applies.
				return writeUserConfigValue(ctx, userScopedConfigKey(workspaceSettingConfigKey(setting.Module, setting.Key)), value, values)
			}
			key := workspaceSettingConfigKey(setting.Module, setting.Key)
			target := state.Workspace
			creates := false
			if envWrite {
				key = workspaceEnvSettingConfigKey(workspaceEnv, setting.Module, setting.Key)
				// Always check, even when the first phase loaded with the env
				// applied: the env may exist only in the user-level overlay,
				// in which case this write still creates the repo-side env
				// section and should say so.
				creates, target, err = workspaceEnvWriteCreates(ctx, state.Workspace, workspaceEnv, workspaceHere)
				if err != nil {
					return err
				}
			}
			if err := target.
				WithConfigValue(key, value, dagger.WorkspaceWithConfigValueOpts{Values: values, Here: workspaceHere}).
				Export(ctx); err != nil {
				return err
			}
			if creates {
				fmt.Fprintf(cmd.OutOrStdout(), "Created env %q\n", workspaceEnv)
			}
			return nil
		}
	})
}

type workspaceSetting struct {
	Module      string
	Key         string
	Value       string
	Description string
	IsList      bool
}

// workspaceSettingWriteValue maps trailing CLI args onto WithConfigValue's
// value/values split. A single value passes through unchanged so existing
// scalar and comma-separated forms keep their behavior. Multiple values are
// only valid for list settings and are passed as an explicit list so elements
// round-trip exactly, without comma-splitting.
func workspaceSettingWriteValue(setting workspaceSetting, args []string) (string, []string, error) {
	if len(args) == 1 {
		return args[0], nil, nil
	}
	if !setting.IsList {
		return "", nil, fmt.Errorf("setting %q of module %q is not a list and accepts a single value", setting.Key, setting.Module)
	}
	return "", args, nil
}

type workspaceSettingsState struct {
	Workspace *dagger.Workspace
	Module    string
	Settings  []workspaceSetting
}

func loadWorkspaceSettingsState(ctx context.Context, dag *dagger.Client, moduleName string, withIsList bool) (*workspaceSettingsState, error) {
	type settingsModule struct {
		Name     string
		Settings []workspaceSetting
	}
	var modules []settingsModule
	if moduleName == "" {
		var res struct {
			CurrentWorkspace struct {
				Modules []settingsModule
			}
		}
		if err := dag.Do(ctx, &dagger.Request{Query: workspaceSettingsQuery}, &dagger.Response{Data: &res}); err != nil {
			return nil, err
		}
		modules = res.CurrentWorkspace.Modules
	} else {
		query := workspaceModuleSettingsQuery
		if withIsList {
			query = workspaceModuleSettingsQueryWithIsList
		}
		var res struct {
			CurrentWorkspace struct {
				Module settingsModule
			}
		}
		if err := dag.Do(ctx, &dagger.Request{
			Query:     query,
			Variables: map[string]any{"module": moduleName},
		}, &dagger.Response{Data: &res}); err != nil {
			return nil, err
		}
		modules = []settingsModule{res.CurrentWorkspace.Module}
	}

	settings := make([]workspaceSetting, 0)
	for _, module := range modules {
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
	return workspacepkg.JoinConfigPath("modules", moduleName, "settings", settingName)
}

// workspaceEnvSettingConfigKey addresses a setting in an env overlay through
// raw env.<name>.* storage, which withConfigValue writes without requiring the
// env to pre-exist (the write creates it).
func workspaceEnvSettingConfigKey(envName, moduleName, settingName string) string {
	return workspacepkg.JoinConfigPath("env", envName, "modules", moduleName, "settings", settingName)
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
