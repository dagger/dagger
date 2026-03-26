package core

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/dagger/dagger/dagql"
	dagqlintrospection "github.com/dagger/dagger/dagql/introspection"
	"github.com/vektah/gqlparser/v2/ast"
)

// TypeDefsFromSchema constructs []*TypeDef by introspecting the given dagql
// server's schema. Only types present in filterSchema (if non-nil) are
// included; pass nil to include all types.
//
// This is the single source of truth for reconstructing TypeDefs from a
// schema. Both CoreMod.TypeDefs and ServedMods.TypeDefs use it.
func TypeDefsFromSchema(dag *dagql.Server, filterSchema map[string]*ast.Definition) ([]*TypeDef, error) {
	dagqlSchema := dagqlintrospection.WrapSchema(dag.Schema())
	schema := &introspection.Schema{}
	if queryName := dagqlSchema.QueryType().Name(); queryName != nil {
		schema.QueryType.Name = *queryName
	}
	for _, dagqlType := range dagqlSchema.Types() {
		codeGenType, err := DagqlToCodegenType(dagqlType)
		if err != nil {
			return nil, err
		}
		schema.Types = append(schema.Types, codeGenType)
	}
	directives, err := dagqlSchema.Directives()
	if err != nil {
		return nil, err
	}
	for _, dagqlDirective := range directives {
		dd, err := DagqlToCodegenDirectiveDef(dagqlDirective)
		if err != nil {
			return nil, err
		}
		schema.Directives = append(schema.Directives, dd)
	}

	typeDefs := make([]*TypeDef, 0, len(schema.Types))
	for _, introspectionType := range schema.Types {
		if filterSchema != nil {
			if _, has := filterSchema[introspectionType.Name]; !has {
				continue
			}
		}

		switch introspectionType.Kind {
		case introspection.TypeKindObject:
			objTypeDef, ok, err := introspectionObjectToTypeDef(introspectionType, dag)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			typeDefs = append(typeDefs, objTypeDef)

		case introspection.TypeKindInputObject:
			typeDef := &InputTypeDef{
				Name: introspectionType.Name,
			}

			for _, introspectionField := range introspectionType.InputFields {
				field := &FieldTypeDef{
					Name:        introspectionField.Name,
					Description: introspectionField.Description,
					Deprecated:  introspectionField.DeprecationReason,
				}
				fieldType, ok, err := introspectionRefToTypeDef(introspectionField.TypeRef, false, false)
				if err != nil {
					return nil, fmt.Errorf("failed to convert return type: %w", err)
				}
				if !ok {
					continue
				}
				field.TypeDef = fieldType
				typeDef.Fields = append(typeDef.Fields, field)
			}

			typeDefs = append(typeDefs, &TypeDef{
				Kind:    TypeDefKindInput,
				AsInput: dagql.NonNull(typeDef),
			})

		case introspection.TypeKindScalar:
			typedef := &ScalarTypeDef{
				Name:        introspectionType.Name,
				Description: introspectionType.Description,
			}

			// Check for source module via @sourceMap directive on the type.
			if sm := introspectionType.Directives.SourceMap(); sm != nil && sm.Module != "" {
				typedef.SourceModuleName = sm.Module
			}

			typeDefs = append(typeDefs, &TypeDef{
				Kind:     TypeDefKindScalar,
				AsScalar: dagql.NonNull(typedef),
			})

		case introspection.TypeKindEnum:
			typedef := &EnumTypeDef{
				Name:        introspectionType.Name,
				Description: introspectionType.Description,
			}

			// Check for source module via @sourceMap directive on the type.
			if sm := introspectionType.Directives.SourceMap(); sm != nil && sm.Module != "" {
				typedef.SourceModuleName = sm.Module
			}

			for _, value := range introspectionType.EnumValues {
				typedef.Members = append(typedef.Members, &EnumMemberTypeDef{
					Name:        value.Name,
					Value:       value.Directives.EnumValue(),
					Description: value.Description,
					Deprecated:  value.DeprecationReason,
				})
			}

			typeDefs = append(typeDefs, &TypeDef{
				Kind:   TypeDefKindEnum,
				AsEnum: dagql.NonNull(typedef),
			})

		case introspection.TypeKindInterface:
			ifaceDef, ok, err := introspectionInterfaceToTypeDef(introspectionType, dag)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			typeDefs = append(typeDefs, ifaceDef)

		default:
			continue
		}
	}
	return typeDefs, nil
}

// introspectionObjectToTypeDef converts an introspection object type to a core TypeDef.
// Returns (nil, false, nil) if the type should be skipped.
func introspectionObjectToTypeDef(introspectionType *introspection.Type, dag *dagql.Server) (*TypeDef, bool, error) {
	typeDef := &ObjectTypeDef{
		Name:        introspectionType.Name,
		Description: introspectionType.Description,
	}

	// Check for source module via @sourceMap directive on the type.
	if sm := introspectionType.Directives.SourceMap(); sm != nil && sm.Module != "" {
		typeDef.SourceModuleName = sm.Module
		// An object is the "main object" if its name matches the module name.
		if gqlObjectName(sm.Module) == introspectionType.Name {
			typeDef.IsMainObject = true
		}
	}

	isIdable := false
	for _, introspectionField := range introspectionType.Fields {
		if introspectionField.Name == "id" {
			isIdable = true
			continue
		}

		fn := &Function{
			Name:        introspectionField.Name,
			Description: introspectionField.Description,
			Deprecated:  introspectionField.DeprecationReason,
		}

		// Restore source map from the field's @sourceMap directive.
		if sm := introspectionField.Directives.SourceMap(); sm != nil {
			fn.SourceMap = dagql.NonNull(&SourceMap{
				Module:   sm.Module,
				Filename: sm.Filename,
				Line:     sm.Line,
				Column:   sm.Column,
				URL:      sm.URL,
			})
		}

		rtType, ok, err := introspectionRefToTypeDef(introspectionField.TypeRef, false, false)
		if err != nil {
			return nil, false, fmt.Errorf("failed to convert return type: %w", err)
		}
		if !ok {
			continue
		}
		fn.ReturnType = rtType

		// For Query root fields, check if the field was provided by a
		// module (e.g. module constructor or entrypoint proxy). This lets
		// consumers (CLI, shell) distinguish module functions from core API.
		if introspectionType.Name == "Query" {
			if queryType, ok := dag.ObjectType("Query"); ok {
				if spec, ok := queryType.FieldSpec(introspectionField.Name, dag.View); ok && spec.Module != nil {
					fn.SourceModuleName = spec.Module.Name()
				}
			}
		}

		for _, introspectionArg := range introspectionField.Args {
			fnArg := &FunctionArg{
				Name:        introspectionArg.Name,
				Description: introspectionArg.Description,
				Deprecated:  introspectionArg.DeprecationReason,
			}

			if introspectionArg.DefaultValue != nil {
				fnArg.DefaultValue = JSON(*introspectionArg.DefaultValue)
			}

			// Restore metadata from directives that the module SDK
			// attached to this argument (defaultPath, defaultAddress,
			// ignorePatterns, sourceMap).
			if dir := introspectionArg.Directives.Directive("defaultPath"); dir != nil {
				if v := dir.Arg("path"); v != nil {
					fnArg.DefaultPath = *v
				}
			}
			if dir := introspectionArg.Directives.Directive("defaultAddress"); dir != nil {
				if v := dir.Arg("address"); v != nil {
					fnArg.DefaultAddress = *v
				}
			}
			if dir := introspectionArg.Directives.Directive("ignorePatterns"); dir != nil {
				if v := dir.Arg("patterns"); v != nil {
					var patterns []string
					if err := json.Unmarshal([]byte(*v), &patterns); err == nil {
						fnArg.Ignore = patterns
					}
				}
			}
			if sm := introspectionArg.Directives.SourceMap(); sm != nil {
				fnArg.SourceMap = dagql.NonNull(&SourceMap{
					Module:   sm.Module,
					Filename: sm.Filename,
					Line:     sm.Line,
					Column:   sm.Column,
					URL:      sm.URL,
				})
			}

			argType, ok, err := introspectionRefToTypeDef(introspectionArg.TypeRef, false, true)
			if err != nil {
				return nil, false, fmt.Errorf("failed to convert argument type: %w", err)
			}
			if !ok {
				continue
			}
			fnArg.TypeDef = argType

			fn.Args = append(fn.Args, fnArg)
		}

		typeDef.Functions = append(typeDef.Functions, fn)
	}

	if !isIdable && typeDef.Name != "Query" {
		return nil, false, nil
	}

	return &TypeDef{
		Kind:     TypeDefKindObject,
		AsObject: dagql.NonNull(typeDef),
	}, true, nil
}

// introspectionInterfaceToTypeDef converts an introspection interface type to a core TypeDef.
// Returns (nil, false, nil) if the type should be skipped.
func introspectionInterfaceToTypeDef(introspectionType *introspection.Type, dag *dagql.Server) (*TypeDef, bool, error) {
	typeDef := &InterfaceTypeDef{
		Name:        introspectionType.Name,
		Description: introspectionType.Description,
	}

	// Check for source module via @sourceMap directive on the type.
	if sm := introspectionType.Directives.SourceMap(); sm != nil && sm.Module != "" {
		typeDef.SourceModuleName = sm.Module
	}

	for _, introspectionField := range introspectionType.Fields {
		if introspectionField.Name == "id" {
			continue
		}

		fn := &Function{
			Name:        introspectionField.Name,
			Description: introspectionField.Description,
			Deprecated:  introspectionField.DeprecationReason,
		}

		if sm := introspectionField.Directives.SourceMap(); sm != nil {
			fn.SourceMap = dagql.NonNull(&SourceMap{
				Module:   sm.Module,
				Filename: sm.Filename,
				Line:     sm.Line,
				Column:   sm.Column,
				URL:      sm.URL,
			})
		}

		rtType, ok, err := introspectionRefToTypeDef(introspectionField.TypeRef, false, false)
		if err != nil {
			return nil, false, fmt.Errorf("failed to convert return type: %w", err)
		}
		if !ok {
			continue
		}
		fn.ReturnType = rtType

		for _, introspectionArg := range introspectionField.Args {
			fnArg := &FunctionArg{
				Name:        introspectionArg.Name,
				Description: introspectionArg.Description,
				Deprecated:  introspectionArg.DeprecationReason,
			}

			if introspectionArg.DefaultValue != nil {
				fnArg.DefaultValue = JSON(*introspectionArg.DefaultValue)
			}

			if sm := introspectionArg.Directives.SourceMap(); sm != nil {
				fnArg.SourceMap = dagql.NonNull(&SourceMap{
					Module:   sm.Module,
					Filename: sm.Filename,
					Line:     sm.Line,
					Column:   sm.Column,
					URL:      sm.URL,
				})
			}

			argType, ok, err := introspectionRefToTypeDef(introspectionArg.TypeRef, false, true)
			if err != nil {
				return nil, false, fmt.Errorf("failed to convert argument type: %w", err)
			}
			if !ok {
				continue
			}
			fnArg.TypeDef = argType

			fn.Args = append(fn.Args, fnArg)
		}

		typeDef.Functions = append(typeDef.Functions, fn)
	}

	return &TypeDef{
		Kind:        TypeDefKindInterface,
		AsInterface: dagql.NonNull(typeDef),
	}, true, nil
}

func introspectionRefToTypeDef(introspectionType *introspection.TypeRef, nonNull, isInput bool) (*TypeDef, bool, error) {
	switch introspectionType.Kind {
	case introspection.TypeKindNonNull:
		return introspectionRefToTypeDef(introspectionType.OfType, true, isInput)

	case introspection.TypeKindScalar:
		if isInput && strings.HasSuffix(introspectionType.Name, "ID") {
			// convert ID inputs to the actual object
			objName := strings.TrimSuffix(introspectionType.Name, "ID")
			return &TypeDef{
				Kind:     TypeDefKindObject,
				Optional: !nonNull,
				AsObject: dagql.NonNull(&ObjectTypeDef{
					Name: objName,
				}),
			}, true, nil
		}

		typeDef := &TypeDef{
			Optional: !nonNull,
		}
		switch introspectionType.Name {
		case string(introspection.ScalarString):
			typeDef.Kind = TypeDefKindString
		case string(introspection.ScalarInt):
			typeDef.Kind = TypeDefKindInteger
		case string(introspection.ScalarFloat):
			typeDef.Kind = TypeDefKindFloat
		case string(introspection.ScalarBoolean):
			typeDef.Kind = TypeDefKindBoolean
		case string(introspection.ScalarVoid):
			typeDef.Kind = TypeDefKindVoid
		default:
			// assume that all core scalars are strings
			typeDef.Kind = TypeDefKindScalar
			typeDef.AsScalar = dagql.NonNull(NewScalarTypeDef(introspectionType.Name, ""))
		}

		return typeDef, true, nil

	case introspection.TypeKindEnum:
		return &TypeDef{
			Kind:     TypeDefKindEnum,
			Optional: !nonNull,
			AsEnum: dagql.NonNull(&EnumTypeDef{
				Name: introspectionType.Name,
			}),
		}, true, nil

	case introspection.TypeKindList:
		elementTypeDef, ok, err := introspectionRefToTypeDef(introspectionType.OfType, false, isInput)
		if err != nil {
			return nil, false, fmt.Errorf("failed to convert list element type: %w", err)
		}
		if !ok {
			return nil, false, nil
		}
		return &TypeDef{
			Kind:     TypeDefKindList,
			Optional: !nonNull,
			AsList: dagql.NonNull(&ListTypeDef{
				ElementTypeDef: elementTypeDef,
			}),
		}, true, nil

	case introspection.TypeKindObject:
		return &TypeDef{
			Kind:     TypeDefKindObject,
			Optional: !nonNull,
			AsObject: dagql.NonNull(&ObjectTypeDef{
				Name: introspectionType.Name,
			}),
		}, true, nil

	case introspection.TypeKindInputObject:
		return &TypeDef{
			Kind:     TypeDefKindInput,
			Optional: !nonNull,
			AsInput: dagql.NonNull(&InputTypeDef{
				Name: introspectionType.Name,
			}),
		}, true, nil

	case introspection.TypeKindInterface:
		return &TypeDef{
			Kind:        TypeDefKindInterface,
			Optional:    !nonNull,
			AsInterface: dagql.NonNull(&InterfaceTypeDef{
				Name: introspectionType.Name,
			}),
		}, true, nil

	default:
		return nil, false, fmt.Errorf("unexpected type kind %s", introspectionType.Kind)
	}
}
