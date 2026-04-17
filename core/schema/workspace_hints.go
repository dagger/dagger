package schema

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
)

func (s *workspaceSchema) collectWorkspaceConfigHints(
	ctx context.Context,
	ws *core.Workspace,
	refs map[string]string,
) map[string][]workspace.ConstructorArgHint {
	if len(refs) == 0 {
		return nil
	}

	ctx, err := withWorkspaceClientContext(ctx, ws)
	if err != nil {
		slog.Warn("could not prepare workspace config hints", "error", err)
		return nil
	}

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		slog.Warn("could not prepare workspace config hints", "error", err)
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
			slog.Warn("could not introspect constructor args for workspace config hints",
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

	mainObj, ok := mod.Self().MainObject()
	if !ok || !mainObj.Constructor.Valid {
		return nil, nil
	}

	hints := make([]workspace.ConstructorArgHint, 0, len(mainObj.Constructor.Value.Args))
	for _, arg := range mainObj.Constructor.Value.Args {
		hints = append(hints, buildHintFromArg(arg))
	}
	return hints, nil
}

var configurableObjectTypes = map[string]string{
	"Container":     `"alpine:latest"`,
	"Directory":     `"./path"`,
	"File":          `"./file"`,
	"Secret":        `"env://MY_SECRET"`,
	"GitRepository": `"https://github.com/owner/repo"`,
	"GitRef":        `"https://github.com/owner/repo#main"`,
	"Service":       `"tcp://localhost:8080"`,
	"Socket":        `"unix:///var/run/docker.sock"`,
}

func buildHintFromArg(arg *core.FunctionArg) workspace.ConstructorArgHint {
	typeLabel, exampleValue, configurable := typeInfoFromTypeDef(arg.TypeDef)
	if arg.DefaultValue != nil {
		if formatted := formatDefaultAsToml(arg.DefaultValue); formatted != "" {
			exampleValue = formatted
		}
	}
	if !configurable {
		typeLabel += " (not configurable via settings)"
	}
	return workspace.ConstructorArgHint{
		Name:         arg.Name,
		TypeLabel:    typeLabel,
		ExampleValue: exampleValue,
	}
}

func typeInfoFromTypeDef(td *core.TypeDef) (typeLabel, exampleValue string, configurable bool) {
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
		if td.AsEnum.Valid {
			return td.AsEnum.Value.Name, `""`, true
		}
		return "enum", `""`, true
	case core.TypeDefKindScalar:
		if td.AsScalar.Valid {
			return td.AsScalar.Value.Name, `""`, true
		}
		return "scalar", `""`, true
	case core.TypeDefKindObject:
		if td.AsObject.Valid {
			objName := td.AsObject.Value.Name
			if example, ok := configurableObjectTypes[objName]; ok {
				return objName, example, true
			}
			return objName, `"..."`, false
		}
		return "object", `"..."`, false
	case core.TypeDefKindList:
		if td.AsList.Valid {
			elemLabel, _, elemConfigurable := typeInfoFromTypeDef(td.AsList.Value.ElementTypeDef)
			example := `["..."]`
			switch {
			case elemConfigurable && td.AsList.Value.ElementTypeDef.Kind == core.TypeDefKindBoolean:
				example = "[false]"
			case elemConfigurable && td.AsList.Value.ElementTypeDef.Kind == core.TypeDefKindInteger:
				example = "[0]"
			case elemConfigurable && td.AsList.Value.ElementTypeDef.Kind == core.TypeDefKindFloat:
				example = "[0.0]"
			case elemConfigurable && td.AsList.Value.ElementTypeDef.Kind == core.TypeDefKindString:
				example = `[""]`
			}
			return "[]" + elemLabel, example, elemConfigurable
		}
		return "list", `["..."]`, false
	default:
		return string(td.Kind), `"..."`, false
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
