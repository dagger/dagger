package schema

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
)

type constructorArgHint struct {
	Name         string
	TypeLabel    string
	IsString     bool
	IsList       bool
	IsObject     bool
	Description  string
	ExampleValue string
	// DefaultValue is the constructor default in the same output form that
	// config reads use (bare strings, [a, b] lists), or empty when there is
	// no default.
	DefaultValue string
}

func workspaceSettingsHintIntrospectionContext(
	ctx context.Context,
	ws *core.Workspace,
) (context.Context, *dagql.Server, error) {
	ctx, err := withWorkspaceClientContext(ctx, ws)
	if err != nil {
		return nil, nil, err
	}

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, nil, err
	}

	return ctx, srv, nil
}

func introspectModule(
	ctx context.Context,
	srv *dagql.Server,
	ref string,
) (*core.Module, error) {
	var mod dagql.ObjectResult[*core.Module]
	if err := srv.Select(ctx, srv.Root(), &mod,
		dagql.Selector{
			Field: "moduleSource",
			Args: []dagql.NamedInput{
				{Name: "refString", Value: dagql.String(ref)},
				{Name: "disableFindUp", Value: dagql.Boolean(true)},
			},
		},
		dagql.Selector{Field: "asModule"},
	); err != nil {
		return nil, fmt.Errorf("loading module: %w", err)
	}
	return mod.Self(), nil
}

func introspectModuleFromDirectory(
	ctx context.Context,
	srv *dagql.Server,
	dir dagql.ObjectResult[*core.Directory],
	sourceRootPath string,
) (*core.Module, error) {
	sourceRootPath = path.Clean(filepath.ToSlash(sourceRootPath))
	if sourceRootPath == "" {
		sourceRootPath = "."
	}

	var mod dagql.ObjectResult[*core.Module]
	if err := srv.Select(ctx, dir, &mod, dagql.Selector{
		Field: "asModule",
		Args: []dagql.NamedInput{
			{Name: "sourceRootPath", Value: dagql.String(sourceRootPath)},
		},
	}); err != nil {
		return nil, fmt.Errorf("loading module from directory: %w", err)
	}
	return mod.Self(), nil
}

// settingHintForKey returns the constructor hint of the module setting that
// key addresses (modules.<m>.settings.<k> or
// env.<e>.modules.<m>.settings.<k>). The lookup is best effort: when key is
// not a module setting, or the module is not installed, has no source, or
// cannot be introspected, there is no hint and the caller writes the value as
// given.
func (s *workspaceSchema) settingHintForKey(
	ctx context.Context,
	ws *core.Workspace,
	staged *stagedWorkspaceConfig,
	key string,
) (string, constructorArgHint, bool) {
	parts, err := workspace.SplitConfigPath(key)
	if err != nil {
		// the write itself reports bad keys
		return "", constructorArgHint{}, false
	}
	envName := ""
	if len(parts) == 6 && parts[0] == "env" {
		envName = parts[1]
		parts = parts[2:]
	}
	if len(parts) != 4 || parts[0] != "modules" || parts[2] != "settings" {
		return "", constructorArgHint{}, false
	}
	hint, ok := s.lookupSettingHint(ctx, ws, staged, envName, parts[1], parts[3])
	return parts[1], hint, ok
}

// writeSettingValue writes value to a module setting in the TOML type its
// hint gives it: an array for a list, where a malformed value is an error, and
// a string for a string or an address, so that 1.27 is not stored as a float.
// Settings of other types are typed from the value.
func writeSettingValue(data []byte, key, moduleName string, hint constructorArgHint, value string) ([]byte, error) {
	switch {
	case hint.IsList:
		elements, err := workspace.ParseListValue(value)
		if err != nil {
			return nil, fmt.Errorf("setting %q of module %q is a list: %w", hint.Name, moduleName, err)
		}
		return workspace.WriteConfigValues(data, key, elements)
	case hint.IsString || hint.IsObject:
		return workspace.WriteConfigStringValue(data, key, value)
	default:
		return workspace.WriteConfigValue(data, key, value)
	}
}

// lookupSettingHint introspects the named module from the staged config and
// returns the constructor hint for settingName. Overlays apply in module-load
// order (user-level, then the env being written to) so modules an overlay
// adds resolve too. Any failure reports no hint.
func (s *workspaceSchema) lookupSettingHint(
	ctx context.Context,
	ws *core.Workspace,
	staged *stagedWorkspaceConfig,
	envName, moduleName, settingName string,
) (constructorArgHint, bool) {
	cfg, err := workspace.ApplyUserOverlay(staged.Config, ws.UserConfigOverlay())
	if err != nil {
		return constructorArgHint{}, false
	}
	if envName != "" {
		if applied, err := workspace.ApplyEnvOverlay(cfg, envName); err == nil {
			cfg = applied
		}
	}
	entry, ok := cfg.Modules[moduleName]
	if !ok || entry.Source == "" {
		return constructorArgHint{}, false
	}

	ctx, srv, err := workspaceSettingsHintIntrospectionContext(ctx, ws)
	if err != nil {
		return constructorArgHint{}, false
	}
	mod, err := introspectWorkspaceModule(ctx, srv, ws, staged.ConfigDir, entry.Source)
	if err != nil {
		return constructorArgHint{}, false
	}
	for _, hint := range constructorHintsFromModule(mod) {
		if strings.EqualFold(hint.Name, settingName) {
			return hint, true
		}
	}
	return constructorArgHint{}, false
}

// mainObjectFunctionNames lists the main object's functions in GraphQL field form, sorted.
func mainObjectFunctionNames(mod *core.Module) []string {
	if mod == nil {
		return nil
	}
	mainObj, ok := mod.MainObject()
	if !ok {
		return nil
	}
	names := make([]string, 0, len(mainObj.Functions))
	for _, fn := range mainObj.Functions {
		names = append(names, fn.Self().Name)
	}
	sort.Strings(names)
	return names
}

func constructorHintsFromModule(mod *core.Module) []constructorArgHint {
	if mod == nil {
		return nil
	}

	mainObj, ok := mod.MainObject()
	if !ok || !mainObj.Constructor.Valid {
		return nil
	}

	constructor := mainObj.Constructor.Value.Self()
	if constructor == nil {
		return nil
	}

	hints := make([]constructorArgHint, 0, len(constructor.Args))
	for _, argResult := range constructor.Args {
		arg := argResult.Self()
		if arg == nil {
			continue
		}
		hint, ok := buildHintFromArg(arg)
		if !ok {
			continue
		}
		hints = append(hints, hint)
	}
	return hints
}

var addressSupportedObjectSettingExamples = map[string]string{ //nolint:gosec
	"Container":     `"alpine:latest"`,
	"Volume":        `"engine-volume://data"`,
	"Directory":     `"./path"`,
	"File":          `"./file"`,
	"Secret":        `"env://MY_SECRET"`,
	"GitRepository": `"https://github.com/owner/repo"`,
	"GitRef":        `"https://github.com/owner/repo#main"`,
	"Service":       `"tcp://localhost:8080"`,
	"Socket":        `"unix:///var/run/docker.sock"`,
}

func buildHintFromArg(arg *core.FunctionArg) (constructorArgHint, bool) {
	typeLabel, exampleValue, configurable := typeInfoFromTypeDef(arg.TypeDef.Self())
	if !configurable {
		return constructorArgHint{}, false
	}
	if arg.DefaultValue != nil {
		if formatted := formatDefaultAsToml(arg.DefaultValue); formatted != "" {
			exampleValue = formatted
		}
	}
	return constructorArgHint{
		Name:         arg.Name,
		TypeLabel:    typeLabel,
		IsString:     arg.TypeDef.Self().Kind == core.TypeDefKindString,
		IsList:       arg.TypeDef.Self().Kind == core.TypeDefKindList,
		IsObject:     arg.TypeDef.Self().Kind == core.TypeDefKindObject,
		Description:  arg.Description,
		ExampleValue: exampleValue,
		DefaultValue: formatDefaultAsOutput(arg.DefaultValue),
	}, true
}

func typeInfoFromTypeDef(td *core.TypeDef) (typeLabel, exampleValue string, configurable bool) { //nolint:gocyclo
	if td == nil {
		return "", "", false
	}

	if isWorkspaceSettingScalarKind(td.Kind) {
		switch td.Kind {
		case core.TypeDefKindString:
			return "string", `""`, true
		case core.TypeDefKindInteger:
			return "int", "0", true
		case core.TypeDefKindFloat:
			return "float", "0.0", true
		case core.TypeDefKindBoolean:
			return "bool", "false", true
		case core.TypeDefKindEnum:
			if td.AsEnum.Valid && td.AsEnum.Value.Self() != nil {
				return td.AsEnum.Value.Self().Name, `""`, true
			}
			return "enum", `""`, true
		case core.TypeDefKindScalar:
			if td.AsScalar.Valid && td.AsScalar.Value.Self() != nil {
				return td.AsScalar.Value.Self().Name, `""`, true
			}
			return "scalar", `""`, true
		}
	}

	switch td.Kind {
	case core.TypeDefKindObject:
		if td.AsObject.Valid && td.AsObject.Value.Self() != nil {
			objName := td.AsObject.Value.Self().Name
			if example, ok := addressSupportedObjectSettingExamples[objName]; ok {
				return objName, example, true
			}
		}
	case core.TypeDefKindList:
		if td.AsList.Valid && td.AsList.Value.Self() != nil {
			elemTypeDef := td.AsList.Value.Self().ElementTypeDef.Self()
			elemLabel, _, elemConfigurable := listElementTypeInfoFromTypeDef(elemTypeDef)
			example := `["..."]`
			switch {
			case elemConfigurable && elemTypeDef != nil && elemTypeDef.Kind == core.TypeDefKindBoolean:
				example = "[false]"
			case elemConfigurable && elemTypeDef != nil && elemTypeDef.Kind == core.TypeDefKindInteger:
				example = "[0]"
			case elemConfigurable && elemTypeDef != nil && elemTypeDef.Kind == core.TypeDefKindFloat:
				example = "[0.0]"
			case elemConfigurable && elemTypeDef != nil && elemTypeDef.Kind == core.TypeDefKindString:
				example = `[""]`
			}
			return "[]" + elemLabel, example, elemConfigurable
		}
	}
	return "", "", false
}

func listElementTypeInfoFromTypeDef(td *core.TypeDef) (typeLabel, exampleValue string, configurable bool) {
	if td != nil && isWorkspaceSettingScalarKind(td.Kind) {
		return typeInfoFromTypeDef(td)
	}
	return "", "", false
}

func isWorkspaceSettingScalarKind(kind core.TypeDefKind) bool {
	switch kind {
	case core.TypeDefKindString,
		core.TypeDefKindInteger,
		core.TypeDefKindFloat,
		core.TypeDefKindBoolean,
		core.TypeDefKindEnum,
		core.TypeDefKindScalar:
		return true
	default:
		return false
	}
}

func formatDefaultAsToml(defaultValue core.JSON) string {
	raw := defaultValue.Bytes()
	if len(raw) == 0 {
		return ""
	}

	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()

	var value any
	if err := dec.Decode(&value); err != nil {
		return ""
	}

	switch v := value.(type) {
	case string:
		return fmt.Sprintf("%q", v)
	case bool:
		if v {
			return "true"
		}
		return "false"
	case json.Number:
		return v.String()
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			formatted := formatDefaultScalarAsToml(item)
			if formatted == "" {
				return ""
			}
			parts = append(parts, formatted)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case nil:
		return ""
	default:
		return ""
	}
}

// formatDefaultAsOutput renders a constructor default the way config reads
// render a stored value: strings bare, lists as [a, b]. Defaults that are not
// flat scalars or scalar lists render as empty.
func formatDefaultAsOutput(defaultValue core.JSON) string {
	raw := defaultValue.Bytes()
	if len(raw) == 0 {
		return ""
	}

	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()

	var value any
	if err := dec.Decode(&value); err != nil {
		return ""
	}

	switch v := value.(type) {
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			formatted, ok := formatDefaultScalarAsOutput(item)
			if !ok {
				return ""
			}
			parts = append(parts, formatted)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	default:
		formatted, _ := formatDefaultScalarAsOutput(v)
		return formatted
	}
}

func formatDefaultScalarAsOutput(value any) (string, bool) {
	switch v := value.(type) {
	case string:
		return v, true
	case bool:
		if v {
			return "true", true
		}
		return "false", true
	case json.Number:
		return v.String(), true
	default:
		return "", false
	}
}

func formatDefaultScalarAsToml(value any) string {
	switch v := value.(type) {
	case string:
		return fmt.Sprintf("%q", v)
	case bool:
		if v {
			return "true"
		}
		return "false"
	case json.Number:
		return v.String()
	default:
		return ""
	}
}
