package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"sort"
	"strings"

	"github.com/dagger/dagger/dagql"
	"github.com/vektah/gqlparser/v2/ast"
)

// loadStaticMethodCallingTools adds ListMethods, SelectMethods, CallMethod,
// and ChainMethods tools.
func (m *MCP) loadStaticMethodCallingTools(srv *dagql.Server, allTools *LLMToolSet, objectMethods *LLMToolSet) {
	allTools.Add(LLMTool{
		Name:        "ListMethods",
		Description: "List the methods that can be selected.",
		ReadOnly:    true,
		Schema: map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"required":             []string{},
			"additionalProperties": false,
		},
		Strict: true,
		Call:   m.listMethodsTool(srv, objectMethods),
	})

	allTools.Add(LLMTool{
		Name:        "SelectMethods",
		Description: "Select methods for interacting with the available objects. Never guess - only select methods previously returned by ListMethods.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"methods": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type":        "string",
						"description": "The name of the method to select, as seen in ListMethods.",
					},
					"description": "The methods to select.",
				},
			},
			"required":             []string{"methods"},
			"additionalProperties": false,
		},
		Strict: true,
		Call:   m.selectMethodsTool(srv, objectMethods),
	})

	allTools.Add(LLMTool{
		Name:        "CallMethod",
		Description: "Call a method on an object. Methods must be selected with SelectMethods before calling them. Self represents the object to call the method on, and args specify any additional parameters to pass.",
		HideSelf:    true,
		ReadOnly:    false,
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"method": map[string]any{
					"type":        "string",
					"description": "The name of the method to call.",
				},
				"self": map[string]any{
					"type":        []string{"string", "null"},
					"description": "The object to call the method on. Not specified for top-level methods.",
				},
				"args": map[string]any{
					"type":                 []string{"object", "null"},
					"description":          "The arguments to pass to the method.",
					"additionalProperties": true,
				},
			},
			"required":             []string{"method", "self", "args"},
			"additionalProperties": false,
		},
		Strict: false,
		Call:   m.callMethodTool(objectMethods),
	})

	allTools.Add(LLMTool{
		Name: "ChainMethods",
		Description: `Invoke multiple methods sequentially, passing the result of one method as the receiver of the next

NOTE: you must select methods before chaining them`,
		HideSelf: true,
		ReadOnly: false,
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"self": map[string]any{
					"type":        []string{"string", "null"},
					"description": "The object to call the method on. Not specified for top-level methods.",
				},
				"chain": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"method": map[string]any{
								"type":        "string",
								"description": "The name of the method to call.",
							},
							"args": map[string]any{
								"type":                 "object",
								"description":          "The arguments to pass to the method.",
								"additionalProperties": true,
							},
						},
						"required": []string{"method", "args"},
					},
					"description": "The chain of method calls.",
				},
			},
			"required":             []string{"chain", "self"},
			"additionalProperties": false,
		},
		Strict: false,
		Call:   m.chainMethodsTool(srv, objectMethods),
	})
}

func (m *MCP) listMethodsTool(srv *dagql.Server, objectMethods *LLMToolSet) LLMToolFunc {
	return ToolFunc(srv, func(ctx context.Context, args struct{}) (any, error) {
		type toolDesc struct {
			Name         string            `json:"name"`
			Returns      string            `json:"returns"`
			RequiredArgs map[string]string `json:"required_args,omitempty"`
		}
		var methods []toolDesc
		for _, method := range objectMethods.Order {
			reqArgs := map[string]string{}
			var returns string
			if method.Field != nil {
				returns = method.Field.Type.String()
				for _, arg := range method.Field.Arguments {
					if arg.DefaultValue != nil || !arg.Type.NonNull {
						continue
					}
					reqArgs[arg.Name] = arg.Type.String()
				}
			}
			methods = append(methods, toolDesc{
				Name:         method.Name,
				RequiredArgs: reqArgs,
				Returns:      returns,
			})
		}
		sort.Slice(methods, func(i, j int) bool {
			return methods[i].Name < methods[j].Name
		})
		return toolStructuredResponse(methods)
	})
}

func (m *MCP) selectMethodsTool(srv *dagql.Server, objectMethods *LLMToolSet) LLMToolFunc {
	return ToolFunc(srv, func(ctx context.Context, args struct {
		Methods []string
	}) (any, error) {
		methodCounts := make(map[string]int)
		for _, toolName := range args.Methods {
			methodCounts[toolName]++
		}
		for tool, count := range methodCounts {
			if count > 1 {
				return "", fmt.Errorf("tool %s selected more than once (%d times)", tool, count)
			}
		}
		type methodDef struct {
			Name        string         `json:"name"`
			Returns     string         `json:"returns,omitempty"`
			Description string         `json:"description"`
			Schema      map[string]any `json:"argsSchema"`
		}
		var selectedMethods []methodDef
		var unknownMethods []string
		for methodName := range methodCounts {
			method, found := objectMethods.Map[methodName]
			if found {
				var returns string
				if method.Field != nil {
					returns = method.Field.Type.String()
				}
				selectedMethods = append(selectedMethods, methodDef{
					Name:        method.Name,
					Returns:     returns,
					Description: method.Description,
					Schema:      method.Schema,
				})
			} else {
				unknownMethods = append(unknownMethods, methodName)
			}
		}
		if len(unknownMethods) > 0 {
			return nil, fmt.Errorf("unknown methods: %v; use ListMethods first", unknownMethods)
		}
		for _, method := range selectedMethods {
			m.selectedMethods[method.Name] = true
		}
		sort.Slice(selectedMethods, func(i, j int) bool {
			return selectedMethods[i].Name < selectedMethods[j].Name
		})
		res := map[string]any{
			"added_methods": selectedMethods,
		}
		if len(unknownMethods) > 0 {
			res["unknown_methods"] = unknownMethods
		}
		return toolStructuredResponse(res)
	})
}

func (m *MCP) callMethodTool(objectMethods *LLMToolSet) LLMToolFunc {
	return func(ctx context.Context, argsAny any) (_ any, rerr error) {
		var call struct {
			Self   string         `json:"self"`
			Method string         `json:"method"`
			Args   map[string]any `json:"args"`
		}
		pl, err := json.Marshal(argsAny)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(pl, &call); err != nil {
			return nil, err
		}
		if call.Args == nil {
			call.Args = make(map[string]any)
		}
		if call.Self != "" {
			call.Args["self"] = call.Self
			matches := idRegex.FindStringSubmatch(call.Self)
			if matches == nil {
				return nil, fmt.Errorf("invalid ID format: %q", call.Self)
			}
			typeName := matches[idRegex.SubexpIndex("type")]
			if !strings.Contains(call.Method, "_") {
				call.Method = fmt.Sprintf("%s_%s", typeName, call.Method)
			}
		}
		var method LLMTool
		method, found := objectMethods.Map[call.Method]
		if !found {
			return nil, fmt.Errorf("method not defined: %q; use ListMethods first", call.Method)
		}
		if !m.selectedMethods[call.Method] {
			return nil, fmt.Errorf("method not selected: %q; use SelectMethods first", call.Method)
		}
		return method.Call(ctx, call.Args)
	}
}

func (m *MCP) chainMethodsTool(srv *dagql.Server, objectMethods *LLMToolSet) LLMToolFunc {
	schema := srv.Schema()
	return func(ctx context.Context, argsAny any) (_ any, rerr error) {
		var toolArgs struct {
			Self  string        `json:"self"`
			Chain []ChainedCall `json:"chain"`
		}
		pl, err := json.Marshal(argsAny)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(pl, &toolArgs); err != nil {
			return nil, err
		}
		if err := m.validateAndNormalizeChain(ctx, toolArgs.Self, toolArgs.Chain, objectMethods, schema); err != nil {
			return nil, err
		}
		var res any
		for i, call := range toolArgs.Chain {
			var tool LLMTool
			tool, found := objectMethods.Map[call.Method]
			if !found {
				return nil, fmt.Errorf("tool not found: %q", call.Method)
			}
			if call.Args == nil {
				call.Args = make(map[string]any)
			}
			args := maps.Clone(call.Args)
			if i > 0 {
				if obj, ok := dagql.UnwrapAs[dagql.AnyObjectResult](m.lastResult); ok {
					args["self"] = m.objs.Track(obj, "")
				}
			} else {
				args["self"] = toolArgs.Self
			}
			res, err = tool.Call(ctx, args)
			if err != nil {
				return nil, fmt.Errorf("call %q: %w", call.Method, err)
			}
		}
		return res, nil
	}
}

type ChainedCall struct {
	Method string         `json:"method"`
	Args   map[string]any `json:"args"`
}

func (m *MCP) validateAndNormalizeChain(ctx context.Context, self string, calls []ChainedCall, objectMethods *LLMToolSet, schema *ast.Schema) error {
	if len(calls) == 0 {
		return errors.New("no methods called")
	}
	var currentType *ast.Type
	if self != "" {
		obj, err := m.GetObject(ctx, self, "")
		if err != nil {
			return err
		}
		currentType = obj.Type()
	}
	var errs error
	for i, call := range calls {
		if call.Method == "" {
			errs = errors.Join(errs, fmt.Errorf("calls[%d]: method name cannot be empty", i))
			continue
		}
		if !strings.Contains(call.Method, "_") && currentType != nil {
			call.Method = currentType.Name() + "_" + call.Method
			calls[i] = call
		}
		method, found := objectMethods.Map[call.Method]
		if !found {
			errs = errors.Join(errs, fmt.Errorf("calls[%d]: unknown method: %q", i, call.Method))
			continue
		}
		if !m.selectedMethods[method.Name] {
			errs = errors.Join(errs, fmt.Errorf("calls[%d]: method %q is not selected", i, method.Name))
		}
		if currentType != nil {
			if currentType.Elem != nil {
				errs = errors.Join(errs, fmt.Errorf("calls[%d]: cannot chain %q call from array result", i, method.Name))
				continue
			}
			typeDef, found := schema.Types[currentType.Name()]
			if !found {
				errs = errors.Join(errs, fmt.Errorf("calls[%d]: unknown type: %q", i, currentType.Name()))
				continue
			}
			if typeDef.Kind != ast.Object {
				errs = errors.Join(errs, fmt.Errorf("calls[%d]: cannot chain %q call from non-Object type: %q (%s)", i, method.Name, currentType.Name(), typeDef.Kind))
			}
		}
		currentType = method.Field.Type
	}
	return errs
}
