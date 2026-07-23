package schema

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

type constructorArgHint struct {
	Name         string
	TypeLabel    string
	IsList       bool
	Description  string
	ExampleValue string
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
) ([]constructorArgHint, error) {
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

func introspectConstructorArgsFromDirectory(
	ctx context.Context,
	srv *dagql.Server,
	dir dagql.ObjectResult[*core.Directory],
	sourceRootPath string,
) ([]constructorArgHint, error) {
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
		IsList:       arg.TypeDef.Self().Kind == core.TypeDefKindList,
		Description:  arg.Description,
		ExampleValue: exampleValue,
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
