package schema

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
)

func (s *workspaceSchema) collectWorkspaceSettingsHints(
	ctx context.Context,
	ws *core.Workspace,
	refs map[string]string,
) map[string][]workspace.ConstructorArgHint {
	if len(refs) == 0 {
		return nil
	}

	ctx, srv, err := workspaceSettingsHintIntrospectionContext(ctx, ws)
	if err != nil {
		slog.Warn("could not prepare workspace settings hints", "error", err)
		return nil
	}

	names := make([]string, 0, len(refs))
	for name := range refs {
		names = append(names, name)
	}
	sort.Strings(names)

	hints := make(map[string][]workspace.ConstructorArgHint, len(refs))
	for _, name := range names {
		ref := refs[name]
		if ref == "" {
			continue
		}

		constructorHints, err := introspectConstructorArgs(ctx, srv, ref)
		if err != nil {
			slog.Warn("could not introspect constructor args for workspace settings hints",
				"module", name,
				"ref", ref,
				"error", err,
			)
			continue
		}
		if len(constructorHints) > 0 {
			hints[name] = constructorHints
		}
	}

	if len(hints) == 0 {
		return nil
	}
	return hints
}

func (s *workspaceSchema) collectWorkspaceSettingsHintsFromConfig(
	ctx context.Context,
	ws *core.Workspace,
	cfg *workspace.Config,
	projectRootPath string,
	migratedDir dagql.ObjectResult[*core.Directory],
) (map[string][]workspace.ConstructorArgHint, []string) {
	if cfg == nil || len(cfg.Modules) == 0 {
		return nil, nil
	}

	ctx, srv, err := workspaceSettingsHintIntrospectionContext(ctx, ws)
	if err != nil {
		slog.Warn("could not prepare workspace settings hints", "error", err)
		return nil, []string{fmt.Sprintf("could not generate workspace settings hints: %v", err)}
	}

	names := make([]string, 0, len(cfg.Modules))
	for name := range cfg.Modules {
		names = append(names, name)
	}
	sort.Strings(names)

	hints := make(map[string][]workspace.ConstructorArgHint, len(cfg.Modules))
	warnings := make([]string, 0)
	for _, name := range names {
		entry, ok := cfg.Modules[name]
		if !ok || entry.Source == "" {
			continue
		}

		constructorHints, err := introspectConfiguredModuleArgs(ctx, srv, projectRootPath, migratedDir, entry.Source)
		if err != nil {
			slog.Warn("could not introspect constructor args for workspace settings hints",
				"module", name,
				"source", entry.Source,
				"error", err,
			)
			warnings = append(warnings, fmt.Sprintf("could not generate workspace settings hints for module %q from source %q: %v", name, entry.Source, err))
			continue
		}
		if len(constructorHints) > 0 {
			hints[name] = constructorHints
		}
	}

	if len(hints) == 0 {
		return nil, warnings
	}
	return hints, warnings
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

func introspectConstructorArgs(
	ctx context.Context,
	srv *dagql.Server,
	ref string,
) ([]workspace.ConstructorArgHint, error) {
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

	return constructorHintsFromModule(mod.Self()), nil
}

func introspectConfiguredModuleArgs(
	ctx context.Context,
	srv *dagql.Server,
	projectRootPath string,
	migratedDir dagql.ObjectResult[*core.Directory],
	source string,
) ([]workspace.ConstructorArgHint, error) {
	resolvedSource := workspace.ResolveModuleEntrySource(workspace.LockDirName, source)
	switch {
	case filepath.IsAbs(resolvedSource):
		return introspectConstructorArgs(ctx, srv, resolvedSource)
	case resolvedSource != source:
		if usesMigratedWorkspaceHintDirectory(resolvedSource) {
			migratedDirID, err := migratedDir.ID()
			if err != nil {
				return nil, err
			}
			if migratedDirID == nil {
				return nil, fmt.Errorf("migrated module source %q requires prepared migrated workspace directory", source)
			}
			return introspectConstructorArgsFromDirectory(ctx, srv, migratedDir, resolvedSource)
		}
		if projectRootPath == "" {
			return nil, fmt.Errorf("workspace project root is required for local module source %q", source)
		}
		return introspectConstructorArgs(ctx, srv, filepath.Clean(filepath.Join(projectRootPath, resolvedSource)))
	default:
		return introspectConstructorArgs(ctx, srv, source)
	}
}

func usesMigratedWorkspaceHintDirectory(resolvedSource string) bool {
	migratedModulesDir := filepath.Clean(filepath.Join(workspace.LockDirName, "modules"))
	resolvedSource = filepath.Clean(resolvedSource)
	return resolvedSource == migratedModulesDir ||
		strings.HasPrefix(resolvedSource, migratedModulesDir+string(filepath.Separator))
}

func introspectConstructorArgsFromDirectory(
	ctx context.Context,
	srv *dagql.Server,
	dir dagql.ObjectResult[*core.Directory],
	sourceRootPath string,
) ([]workspace.ConstructorArgHint, error) {
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

	return constructorHintsFromModule(mod.Self()), nil
}

func constructorHintsFromModule(mod *core.Module) []workspace.ConstructorArgHint {
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

	hints := make([]workspace.ConstructorArgHint, 0, len(constructor.Args))
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

var addressSupportedObjectSettingExamples = map[string]string{
	"Container":     `"alpine:latest"`,
	"Directory":     `"./path"`,
	"File":          `"./file"`,
	"Secret":        `"env://MY_SECRET"`,
	"GitRepository": `"https://github.com/owner/repo"`,
	"GitRef":        `"https://github.com/owner/repo#main"`,
	"Service":       `"tcp://localhost:8080"`,
	"Socket":        `"unix:///var/run/docker.sock"`,
}

func buildHintFromArg(arg *core.FunctionArg) (workspace.ConstructorArgHint, bool) {
	typeLabel, exampleValue, configurable := typeInfoFromTypeDef(arg.TypeDef.Self())
	if !configurable {
		return workspace.ConstructorArgHint{}, false
	}
	if arg.DefaultValue != nil {
		if formatted := formatDefaultAsToml(arg.DefaultValue); formatted != "" {
			exampleValue = formatted
		}
	}
	return workspace.ConstructorArgHint{
		Name:         arg.Name,
		TypeLabel:    typeLabel,
		Description:  arg.Description,
		ExampleValue: exampleValue,
	}, true
}

func typeInfoFromTypeDef(td *core.TypeDef) (typeLabel, exampleValue string, configurable bool) {
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
