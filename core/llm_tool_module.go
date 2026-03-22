package core

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/dagger/dagger/dagql"
	"github.com/iancoleman/strcase"
	"github.com/vektah/gqlparser/v2/ast"
)

// loadModuleTools loads tools from installed modules.
func (m *MCP) loadModuleTools(srv *dagql.Server, allTools *LLMToolSet) error {
	if m.env.ID() == nil {
		return nil
	}
	schema := srv.Schema()
	for _, mod := range m.env.Self().installedModules {
		modTypeName := strcase.ToCamel(mod.Name())
		modTypeDef := schema.Types[modTypeName]
		for _, obj := range mod.ObjectDefs {
			def := obj.AsObject.Value
			if strcase.ToCamel(def.Name) != modTypeName {
				continue
			}
			var hasRequiredArgs bool
			if def.Constructor.Valid {
				for _, arg := range def.Constructor.Value.Args {
					if !arg.TypeDef.Optional && arg.DefaultPath == "" && arg.DefaultValue == nil {
						hasRequiredArgs = true
						break
					}
				}
			}
			if hasRequiredArgs {
				return fmt.Errorf("TODO: module %s constructor cannot have required arguments", mod.Name())
			}
			if err := m.typeTools(allTools, srv, schema, modTypeDef, def); err != nil {
				return err
			}
		}
	}
	return nil
}

// loadReachableObjectMethods loads tools for core types reachable from the environment.
func (m *MCP) loadReachableObjectMethods(srv *dagql.Server, allTools *LLMToolSet) error {
	schema := srv.Schema()
	typeNames := m.objs.Types()
	if m.env.ID() != nil && m.env.Self().IsPrivileged() {
		typeNames = append(typeNames, schema.Query.Name)
	}
	for _, typeName := range typeNames {
		typeDef, ok := schema.Types[typeName]
		if !ok {
			return fmt.Errorf("type %q not found", typeName)
		}
		if err := m.typeTools(allTools, srv, schema, typeDef, nil); err != nil {
			return fmt.Errorf("load %q tools: %w", typeName, err)
		}
	}
	return nil
}

// typeTools creates tools for all fields on a type.
func (m *MCP) typeTools(allTools *LLMToolSet, srv *dagql.Server, schema *ast.Schema, typeDef *ast.Definition, autoConstruct *ObjectTypeDef) error {
	for _, field := range typeDef.Fields {
		if strings.HasPrefix(field.Name, "_") {
			continue
		}
		if strings.HasPrefix(field.Name, "load") && strings.HasSuffix(field.Name, "FromID") {
			continue
		}
		if field.Name == "id" || field.Name == "sync" {
			continue
		}
		if slices.Contains(m.blockedMethods[typeDef.Name], field.Name) {
			continue
		}
		isTrivial := field.Directives.ForName(trivialFieldDirectiveName) != nil
		if isTrivial {
			fieldType := field.Type
			if fieldType.Elem != nil {
				continue
			}
			td, isObject := schema.Types[fieldType.NamedType]
			if !isObject || td.Kind != ast.Object {
				continue
			}
		}
		if field.Directives.ForName(deprecatedDirectiveName) != nil {
			continue
		}
		if references(field, TypesHiddenFromEnvExtensions...) {
			continue
		}

		// Determine whether self parameter is needed
		includeSelf := typeDef.Name != schema.Query.Name && autoConstruct == nil
		toolSchema, err := fieldArgsToJSONSchema(schema, typeDef, field, includeSelf)
		if err != nil {
			return fmt.Errorf("field %q: %w", field.Name, err)
		}

		var toolName string
		if typeDef.Name == schema.Query.Name ||
			(autoConstruct != nil && allTools.Map[toolName].Name == "") {
			toolName = field.Name
		} else {
			toolName = typeDef.Name + "_" + field.Name
		}

		contextual := autoConstruct != nil

		allTools.Add(LLMTool{
			Name:        toolName,
			Field:       field,
			Description: strings.TrimSpace(field.Description),
			Schema:      toolSchema,
			Strict:      false,
			HideSelf:    !contextual,
			ReadOnly:    field.Type.NamedType != "Env" && field.Type.NamedType != "Changeset",
			Call:        m.makeToolCall(srv, schema, typeDef.Name, field, autoConstruct, contextual),
		})
	}
	return nil
}

// makeToolCall creates the Call closure for a type tool.
func (m *MCP) makeToolCall(
	srv *dagql.Server,
	schema *ast.Schema,
	selfType string,
	field *ast.FieldDefinition,
	autoConstruct *ObjectTypeDef,
	contextual bool,
) LLMToolFunc {
	return func(ctx context.Context, args any) (_ any, rerr error) {
		argsMap, ok := args.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid arguments type: %T", args)
		}
		if !contextual {
			ctx = dagql.WithRepeatedTelemetry(ctx)
		} else {
			ctx = dagql.WithNonInternalTelemetry(ctx)
		}
		return m.call(ctx, srv, schema, selfType, field, argsMap, autoConstruct)
	}
}

func references(fieldDef *ast.FieldDefinition, types ...dagql.Typed) bool {
	names := map[string]bool{}
	for _, t := range types {
		names[t.Type().Name()] = true
	}
	if names[fieldDef.Type.Name()] {
		return true
	}
	for _, arg := range fieldDef.Arguments {
		if names[arg.Type.Name()] {
			return true
		}
	}
	return false
}
