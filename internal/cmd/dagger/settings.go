package daggercmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"dagger.io/dagger"
	"github.com/charmbracelet/x/ansi"
	workspacepkg "github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/engine/client"
	"github.com/spf13/cobra"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// workspaceSettingFields lists the setting fields to request, richest first.
// Older engines lack the later additions (defaultValue, isList, isObject),
// so loading falls back through this list on "Cannot query field" errors.
var workspaceSettingFields = []string{
	"key value description defaultValue isList isObject",
	"key value description isList isObject",
	"key value description",
}

func workspaceSettingsQuery(fields string) string {
	return `
query WorkspaceSettings {
  currentWorkspace {
    modules {
      name
      settings { ` + fields + ` }
    }
  }
}
`
}

func workspaceModuleSettingsQuery(fields string) string {
	return `
query WorkspaceModuleSettings($module: String!) {
  currentWorkspace {
    module(name: $module) {
      name
      settings { ` + fields + ` }
    }
  }
}
`
}

const workspaceEntrypointQuery = `
query WorkspaceEntrypoint {
  currentWorkspace {
    modules {
      name
      entrypoint
    }
  }
}
`

const workspaceModuleFunctionsQuery = `
query WorkspaceModuleFunctions($module: String!) {
  currentWorkspace {
    module(name: $module) {
      functions
    }
  }
}
`

var settingsCmd = newSettingsCmd(false)
var settingsAliasCmd = newSettingsCmd(false)

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
		Short:  "Get, set, or unset module settings",
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

		state, err := loadWorkspaceSettingsState(ctx, engineClient.Dagger(), moduleName)
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
			_, err = fmt.Fprintln(cmd.OutOrStdout(), workspaceSettingDisplayValue(setting))
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
			if values == nil {
				value, err = normalizeEntrypointFunctionRef(ctx, engineClient.Dagger(), setting, value)
				if err != nil {
					return err
				}
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
	Module       string
	Key          string
	Value        string
	Description  string
	DefaultValue string
	IsList       bool
	IsObject     bool
}

// workspaceSettingDisplayValue renders a setting for output: the configured
// value when set, otherwise the constructor default marked as such so a
// default is never mistaken for a value the user wrote.
func workspaceSettingDisplayValue(setting workspaceSetting) string {
	if setting.Value != "" || setting.DefaultValue == "" {
		return setting.Value
	}
	return setting.DefaultValue + " (default)"
}

// normalizeEntrypointFunctionRef rewrites a short-form entrypoint function
// reference ("image") to the long form the config stores ("provider:image").
// Only object-typed settings are candidates, so a string setting whose value
// matches a function name is left alone.
func normalizeEntrypointFunctionRef(ctx context.Context, dag *dagger.Client, setting workspaceSetting, value string) (string, error) {
	if !setting.IsObject || !workspacepkg.IsShortFormModuleRef(value) {
		return value, nil
	}

	var modules struct {
		CurrentWorkspace struct {
			Modules []struct {
				Name       string
				Entrypoint bool
			}
		}
	}
	if err := dag.Do(ctx, &dagger.Request{Query: workspaceEntrypointQuery}, &dagger.Response{Data: &modules}); err != nil {
		return "", err
	}
	entrypoint := ""
	for _, module := range modules.CurrentWorkspace.Modules {
		if module.Entrypoint {
			entrypoint = module.Name
			break
		}
	}
	if entrypoint == "" {
		return value, nil
	}

	var functions struct {
		CurrentWorkspace struct {
			Module struct {
				Functions []string
			}
		}
	}
	if err := dag.Do(ctx, &dagger.Request{
		Query:     workspaceModuleFunctionsQuery,
		Variables: map[string]any{"module": entrypoint},
	}, &dagger.Response{Data: &functions}); err != nil {
		return "", err
	}
	want := gqlFieldName(value)
	for _, fn := range functions.CurrentWorkspace.Module.Functions {
		if fn == want {
			return entrypoint + ":" + value, nil
		}
	}
	return value, nil
}

// isUnknownGraphQLFieldError reports whether the engine predates a requested field.
func isUnknownGraphQLFieldError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "Cannot query field")
}

// workspaceSettingWriteValue maps trailing CLI args onto WithConfigValue's
// value/values split. A scalar setting takes a single value, passed through
// unchanged so auto-detection keeps its behavior. A list setting always
// writes an explicit list so the config stores a TOML array: multiple values
// are its elements verbatim, and a single value is parsed into elements
// (comma-separated, optionally bracketed or quoted) so "." stores as ["."]
// rather than ".". An empty list is written as the "[]" value, which the
// engine converts, because the SDK omits an empty values list. Engines that
// predate isList report every setting as scalar, and there the single-value
// form falls back to the string write.
func workspaceSettingWriteValue(setting workspaceSetting, args []string) (string, []string, error) {
	if !setting.IsList {
		if len(args) == 1 {
			return args[0], nil, nil
		}
		return "", nil, fmt.Errorf("setting %q of module %q is not a list and accepts a single value", setting.Key, setting.Module)
	}
	if len(args) > 1 {
		return "", args, nil
	}
	values, err := workspacepkg.ParseListValue(args[0])
	if err != nil {
		return "", nil, fmt.Errorf("setting %q of module %q is a list: %w", setting.Key, setting.Module, err)
	}
	if len(values) == 0 {
		return "[]", values, nil
	}
	return "", values, nil
}

type workspaceSettingsState struct {
	Workspace *dagger.Workspace
	Module    string
	Settings  []workspaceSetting
}

func loadWorkspaceSettingsState(ctx context.Context, dag *dagger.Client, moduleName string) (*workspaceSettingsState, error) {
	type settingsModule struct {
		Name     string
		Settings []workspaceSetting
	}
	var modules []settingsModule
	var err error
	for _, fields := range workspaceSettingFields {
		if moduleName == "" {
			var res struct {
				CurrentWorkspace struct {
					Modules []settingsModule
				}
			}
			err = dag.Do(ctx, &dagger.Request{Query: workspaceSettingsQuery(fields)}, &dagger.Response{Data: &res})
			modules = res.CurrentWorkspace.Modules
		} else {
			var res struct {
				CurrentWorkspace struct {
					Module settingsModule
				}
			}
			err = dag.Do(ctx, &dagger.Request{
				Query:     workspaceModuleSettingsQuery(fields),
				Variables: map[string]any{"module": moduleName},
			}, &dagger.Response{Data: &res})
			modules = []settingsModule{res.CurrentWorkspace.Module}
		}
		if !isUnknownGraphQLFieldError(err) {
			break
		}
	}
	if err != nil {
		return nil, err
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
	return writeWorkspaceSettingsTableAtWidth(out, settings, getViewWidth())
}

const (
	workspaceSettingsColumnPadding = 2
	workspaceSettingsModuleMax     = 20
	workspaceSettingsKeyMax        = 24
	workspaceSettingsValueMax      = 32
	workspaceSettingsValueReserve  = 18
	workspaceSettingsDescReserve   = 24
)

var workspaceSettingsHeaders = []string{"MODULE", "KEY", "VALUE", "DESCRIPTION"}

func writeWorkspaceSettingsTableAtWidth(out io.Writer, settings []workspaceSetting, viewWidth int) error {
	if len(settings) == 0 {
		_, err := fmt.Fprintln(out, "(no settings)")
		return err
	}

	rows := make([][]string, 0, len(settings)+1)
	rows = append(rows, workspaceSettingsHeaders)
	for _, setting := range settings {
		rows = append(rows, []string{
			workspaceSettingSingleLine(setting.Module),
			workspaceSettingSingleLine(setting.Key),
			workspaceSettingSingleLine(workspaceSettingDisplayValue(setting)),
			workspaceSettingSingleLine(workspaceSettingShortDescription(setting.Description)),
		})
	}

	widths := workspaceSettingsColumnWidths(rows, viewWidth)
	for _, row := range rows {
		var line strings.Builder
		for column, cell := range row {
			cell = ansi.Truncate(cell, widths[column], "…")
			line.WriteString(cell)
			if column < len(row)-1 {
				padding := widths[column] - ansi.StringWidth(cell) + workspaceSettingsColumnPadding
				line.WriteString(strings.Repeat(" ", padding))
			}
		}
		if _, err := fmt.Fprintln(out, line.String()); err != nil {
			return err
		}
	}
	return nil
}

func workspaceSettingsColumnWidths(rows [][]string, viewWidth int) []int {
	minimums := make([]int, len(workspaceSettingsHeaders))
	desired := make([]int, len(workspaceSettingsHeaders))
	for column, header := range workspaceSettingsHeaders {
		minimums[column] = ansi.StringWidth(header)
		desired[column] = minimums[column]
	}
	for _, row := range rows[1:] {
		for column, cell := range row {
			desired[column] = max(desired[column], ansi.StringWidth(cell))
		}
	}
	desired[0] = min(desired[0], workspaceSettingsModuleMax)
	desired[1] = min(desired[1], workspaceSettingsKeyMax)
	desired[2] = min(desired[2], workspaceSettingsValueMax)

	paddingWidth := workspaceSettingsColumnPadding * (len(workspaceSettingsHeaders) - 1)
	minimumContentWidth := 0
	for _, width := range minimums {
		minimumContentWidth += width
	}
	contentWidth := max(viewWidth-paddingWidth, minimumContentWidth)

	valueAndDescriptionReserve := min(desired[2], workspaceSettingsValueReserve) +
		min(desired[3], workspaceSettingsDescReserve)
	moduleAndKeyMinimum := minimums[0] + minimums[1]
	moduleAndKeyWidth := max(contentWidth-valueAndDescriptionReserve, moduleAndKeyMinimum)
	moduleWidth, keyWidth := workspaceSettingsFitColumns(
		desired[0], desired[1], minimums[0], minimums[1], moduleAndKeyWidth,
	)
	valueAndDescriptionWidth := contentWidth - moduleWidth - keyWidth
	valueWidth, descriptionWidth := workspaceSettingsFitColumns(
		desired[2], desired[3], minimums[2], minimums[3], valueAndDescriptionWidth,
	)

	return []int{moduleWidth, keyWidth, valueWidth, descriptionWidth}
}

// workspaceSettingsFitColumns reduces two columns fairly until they fit the
// available width. Each column always remains wide enough for its header.
func workspaceSettingsFitColumns(first, second, firstMinimum, secondMinimum, available int) (int, int) {
	if first+second <= available {
		return first, second
	}

	extra := available - firstMinimum - secondMinimum
	firstExtra := min(first-firstMinimum, (extra+1)/2)
	secondExtra := min(second-secondMinimum, extra-firstExtra)
	extra -= firstExtra + secondExtra

	secondExtraMore := min(second-secondMinimum-secondExtra, extra)
	secondExtra += secondExtraMore
	extra -= secondExtraMore
	firstExtra += min(first-firstMinimum-firstExtra, extra)

	return firstMinimum + firstExtra, secondMinimum + secondExtra
}

func workspaceSettingSingleLine(value string) string {
	replacer := strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ", "\t", " ")
	return replacer.Replace(value)
}

func workspaceSettingShortDescription(description string) string {
	return strings.SplitN(description, "\n", 2)[0]
}
