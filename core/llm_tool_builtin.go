package core

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/dagger/dagger/dagql"
)

// loadBuiltins adds built-in tools (Save, DeclareOutput, ListObjects,
// ReadLogs, UserProvidedValues).
func (m *MCP) loadBuiltins(srv *dagql.Server, allTools, objectMethods *LLMToolSet) {
	schema := srv.Schema()
	hasEnv := m.env.ID() != nil

	if hasEnv && m.env.Self().writable {
		allTypes := map[string]dagql.Type{
			"String": dagql.String(""),
		}
		for name := range schema.Types {
			if strings.HasPrefix(name, "_") {
				continue
			}
			objectType, ok := srv.ObjectType(name)
			if !ok {
				continue
			}
			if slices.ContainsFunc(TypesHiddenFromEnvExtensions, func(t dagql.Typed) bool {
				return t.Type().Name() == name
			}) {
				continue
			}
			allTypes[name] = objectType
		}
		allTools.Add(LLMTool{
			Name:        "DeclareOutput",
			Description: "Declare a new output that can have a value saved to it",
			ReadOnly:    false,
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "The name of the output, following shell naming conventions ([a-z][a-z0-9_]*).",
					},
					"type": map[string]any{
						"type":        "string",
						"description": "The type of the output.",
						"enum":        slices.Sorted(maps.Keys(allTypes)),
					},
					"description": map[string]any{
						"type":        []string{"string", "null"},
						"description": "An optional description of the output.",
					},
				},
				"required":             []string{"name", "type", "description"},
				"additionalProperties": false,
			},
			Strict: true,
			Call: ToolFunc(srv, func(ctx context.Context, args struct {
				Name        string
				Type        string
				Description string `default:""`
			}) (any, error) {
				if _, ok := allTypes[args.Type]; !ok {
					return nil, fmt.Errorf("unknown type: %q", args.Type)
				}
				var dest dagql.ObjectResult[*Env]
				err := srv.Select(ctx, m.env, &dest, dagql.Selector{
					View:  srv.View,
					Field: "with" + args.Type + "Output",
					Args: []dagql.NamedInput{
						{
							Name:  "name",
							Value: dagql.String(args.Name),
						},
						{
							Name:  "description",
							Value: dagql.String(args.Description),
						},
					},
				})
				if err != nil {
					return nil, err
				}
				m.env = dest
				return toolStructuredResponse(map[string]any{
					"output": args.Name,
					"hint":   "To save a value to the output, use the Save tool.",
				})
			}),
		})
	}

	if len(m.objs.TypeCounts()) > 0 {
		allTools.Add(LLMTool{
			Name:        "ListObjects",
			Description: "List available objects.",
			ReadOnly:    true,
			Schema: map[string]any{
				"type":                 "object",
				"properties":           map[string]any{},
				"required":             []string{},
				"additionalProperties": false,
			},
			Strict: true,
			Call: ToolFunc(srv, func(ctx context.Context, args struct{}) (any, error) {
				type objDesc struct {
					ID          string `json:"id"`
					Description string `json:"description"`
				}
				var objects []objDesc
				counts := m.objs.TypeCounts()
				for _, typeName := range slices.Sorted(maps.Keys(counts)) {
					count := counts[typeName]
					for i := 1; i <= count; i++ {
						bnd, found, err := m.Input(ctx, fmt.Sprintf("%s#%d", typeName, i))
						if err != nil {
							continue
						}
						if !found {
							continue
						}
						objects = append(objects, objDesc{
							ID:          bnd.ID(),
							Description: bnd.Description,
						})
					}
				}
				return toolStructuredResponse(objects)
			}),
		})
	}

	if m.staticTools {
		m.loadStaticMethodCallingTools(srv, allTools, objectMethods)
	}

	allTools.Add(LLMTool{
		Name:        "ReadLogs",
		Description: "Read logs from the most recent execution. Can filter with grep pattern or read the last N lines.",
		ReadOnly:    true,
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"span": map[string]any{
					"type":        "string",
					"description": "Span ID to query logs beneath, recursively",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Number of lines to read from the end.",
					"minimum":     1,
					"default":     100,
				},
				"offset": map[string]any{
					"type":        "integer",
					"description": "Number of lines to skip from the end. If not specified, starts from the end.",
					"minimum":     0,
				},
				"grep": map[string]any{
					"type":        "string",
					"description": "Grep pattern to filter logs. If specified, only lines matching this pattern will be returned.",
				},
			},
			"required":             []string{"span"},
			"additionalProperties": false,
		},
		Strict: false,
		Call:   m.readLogsTool(srv),
	})

	if hasEnv && len(m.env.Self().outputsByName) > 0 {
		allTools.Add(m.saveTool(srv))
	}

	if hasEnv && len(m.env.Self().inputsByName) > 0 {
		allTools.Add(LLMTool{
			Name:        "UserProvidedValues",
			Description: "Read the inputs supplied by the user.",
			ReadOnly:    true,
			Schema: map[string]any{
				"type":                 "object",
				"properties":           map[string]any{},
				"required":             []string{},
				"additionalProperties": false,
			},
			Strict: true,
			Call: func(ctx context.Context, args any) (any, error) {
				values, err := m.userProvidedValues()
				if err != nil {
					return nil, err
				}
				if values == "" {
					return "No user-provided values.", nil
				}
				return values, nil
			},
		})
	}
}

func (m *MCP) saveTool(srv *dagql.Server) LLMTool {
	desc := "Save an output that has been requested by the user."

	checklist := func() string {
		var list []string
		for name, b := range m.env.Self().outputsByName {
			checked := " "
			if b.Value != nil {
				checked = "x"
			}
			list = append(list,
				fmt.Sprintf("- [%s] %s (%s): %s", checked, name, b.ExpectedType, b.Description))
		}
		sort.Strings(list)
		return strings.Join(list, "\n")
	}

	desc += "\n\nThe following checklist describes the desired outputs:"
	desc += "\n\n" + checklist()

	return LLMTool{
		Name:        "Save",
		Description: desc,
		ReadOnly:    false,
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "The name of the output, following shell naming conventions ([a-z][a-z0-9_]*).",
				},
				"value": map[string]any{
					"type":        "string",
					"description": "The value to save for the output.",
				},
			},
			"required":             []string{"name", "value"},
			"additionalProperties": false,
		},
		Strict: true,
		Call: ToolFunc(srv, func(ctx context.Context, args struct {
			Name  string
			Value string
		}) (any, error) {
			output, ok := m.env.Self().outputsByName[args.Name]
			if !ok {
				return nil, fmt.Errorf("unknown output: %q - please declare it first", args.Name)
			}
			if output.ExpectedType == "String" {
				output.Value = dagql.String(args.Value)
			} else {
				bnd, ok, err := m.Input(ctx, args.Value)
				if err != nil {
					return nil, err
				}
				if !ok {
					return nil, fmt.Errorf("object not found for argument %s: %s", args.Name, args.Value)
				}

				obj := bnd.Value
				actualType := obj.Type().Name()
				if output.ExpectedType != actualType {
					return nil, fmt.Errorf("incompatible types: %s must be %s, got %s", args.Name, output.ExpectedType, actualType)
				}

				bnd.Description = output.Description
				output.Value = obj
			}

			return checklist(), nil
		}),
	}
}

func (m *MCP) readLogsTool(srv *dagql.Server) LLMToolFunc {
	return ToolFunc(srv, func(ctx context.Context, args struct {
		Span   string
		Offset int    `default:"0"`
		Limit  int    `default:"100"`
		Grep   string `default:""`
	}) (any, error) {
		logs, err := captureLogs(ctx, args.Span)
		if err != nil {
			return nil, fmt.Errorf("failed to capture logs: %w", err)
		}

		if args.Offset >= len(logs) {
			return nil, fmt.Errorf("offset %d is beyond log length %d", args.Offset, len(logs))
		}
		logs = logs[:len(logs)-args.Offset]

		if args.Grep != "" {
			re, err := regexp.Compile(args.Grep)
			if err != nil {
				return nil, fmt.Errorf("invalid grep pattern %q: %w", args.Grep, err)
			}
			var filteredLogs []string
			for i, line := range logs {
				if re.MatchString(line) {
					filteredLogs = append(filteredLogs, fmt.Sprintf("%6d→%s", i+1, line))
				}
			}
			logs = filteredLogs
		} else {
			for i, line := range logs {
				logs[i] = fmt.Sprintf("%6d→%s", i+1, line)
			}
		}

		logs = limitLines(args.Span, logs, args.Limit, llmLogsMaxLineLen)

		return strings.Join(logs, "\n"), nil
	})
}

func (m *MCP) userProvidedValues() (string, error) {
	if m.env.ID() == nil {
		return "", nil
	}
	type valueDesc struct {
		Description string `json:"description"`
		Value       any    `json:"value"`
	}
	var values []valueDesc
	for _, input := range m.env.Self().Inputs() {
		description := input.Description
		if description == "" {
			description = input.Key
		}
		if obj, isObj := input.AsObject(); isObj {
			values = append(values, valueDesc{
				Value:       m.objs.Track(obj, input.Description),
				Description: description,
			})
		} else {
			values = append(values, valueDesc{
				Value:       input.Value,
				Description: description,
			})
		}
	}
	if len(values) == 0 {
		return "", nil
	}
	return toolStructuredResponse(values)
}
