package schema

import (
	"context"
	"fmt"
	"time"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

func (s *moduleSchema) inputTypeDef(ctx context.Context, self *core.Query, args struct {
	Name string
}) (*core.TypeDef, error) {
	deps, err := self.CurrentServedDeps(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get current module: %w", err)
	}
	dag, err := deps.Schema(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get current schema: %w", err)
	}
	typeDefs, err := deps.TypeDefs(ctx, dag)
	if err != nil {
		return nil, err
	}
	for _, typeDef := range typeDefs {
		if typeDef == nil || typeDef.Kind != core.TypeDefKindInput || !typeDef.AsInput.Valid {
			continue
		}
		if typeDef.AsInput.Value.Name == args.Name {
			return typeDef, nil
		}
	}
	return nil, fmt.Errorf("input type %q not found", args.Name)
}

func (s *moduleSchema) functionArgs(
	ctx context.Context,
	fn *core.Function,
	_ struct{},
) (dagql.ObjectResultArray[*core.FunctionArg], error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}
	return currentCallObjectResultArray(ctx, dag, fn.Args)
}

func (s *moduleSchema) functionReturnType(
	ctx context.Context,
	fn *core.Function,
	_ struct{},
) (dagql.ObjectResult[*core.TypeDef], error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.TypeDef]{}, fmt.Errorf("failed to get dag server: %w", err)
	}
	return s.canonicalTypeDefRef(ctx, dag, fn.ReturnType)
}

func (s *moduleSchema) typeDefAsList(
	ctx context.Context,
	typeDef *core.TypeDef,
	_ struct{},
) (dagql.Nullable[dagql.ObjectResult[*core.ListTypeDef]], error) {
	return currentCallNullableObjectResult(ctx, typeDef.AsList)
}

func (s *moduleSchema) typeDefAsObject(
	ctx context.Context,
	typeDef *core.TypeDef,
	_ struct{},
) (dagql.Nullable[dagql.ObjectResult[*core.ObjectTypeDef]], error) {
	return currentCallNullableObjectResult(ctx, typeDef.AsObject)
}

func (s *moduleSchema) typeDefAsInterface(
	ctx context.Context,
	typeDef *core.TypeDef,
	_ struct{},
) (dagql.Nullable[dagql.ObjectResult[*core.InterfaceTypeDef]], error) {
	return currentCallNullableObjectResult(ctx, typeDef.AsInterface)
}

func (s *moduleSchema) typeDefAsInput(
	ctx context.Context,
	typeDef *core.TypeDef,
	_ struct{},
) (dagql.Nullable[dagql.ObjectResult[*core.InputTypeDef]], error) {
	return currentCallNullableObjectResult(ctx, typeDef.AsInput)
}

func (s *moduleSchema) typeDefAsScalar(
	ctx context.Context,
	typeDef *core.TypeDef,
	_ struct{},
) (dagql.Nullable[dagql.ObjectResult[*core.ScalarTypeDef]], error) {
	return currentCallNullableObjectResult(ctx, typeDef.AsScalar)
}

func (s *moduleSchema) typeDefAsEnum(
	ctx context.Context,
	typeDef *core.TypeDef,
	_ struct{},
) (dagql.Nullable[dagql.ObjectResult[*core.EnumTypeDef]], error) {
	return currentCallNullableObjectResult(ctx, typeDef.AsEnum)
}

func (s *moduleSchema) functionArgTypeDef(
	ctx context.Context,
	arg *core.FunctionArg,
	_ struct{},
) (dagql.ObjectResult[*core.TypeDef], error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.TypeDef]{}, fmt.Errorf("failed to get dag server: %w", err)
	}
	return s.canonicalTypeDefRef(ctx, dag, arg.TypeDef)
}

func (s *moduleSchema) fieldTypeDefTypeDef(
	ctx context.Context,
	field *core.FieldTypeDef,
	_ struct{},
) (dagql.ObjectResult[*core.TypeDef], error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.TypeDef]{}, fmt.Errorf("failed to get dag server: %w", err)
	}
	return s.canonicalTypeDefRef(ctx, dag, field.TypeDef)
}

func (s *moduleSchema) objectTypeDefFields(
	ctx context.Context,
	obj *core.ObjectTypeDef,
	_ struct{},
) (dagql.ObjectResultArray[*core.FieldTypeDef], error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}
	return currentCallObjectResultArray(ctx, dag, obj.Fields)
}

func (s *moduleSchema) objectTypeDefFunctions(
	ctx context.Context,
	obj *core.ObjectTypeDef,
	_ struct{},
) (dagql.ObjectResultArray[*core.Function], error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}
	arr := make(dagql.ObjectResultArray[*core.Function], 0, len(obj.Functions))
	for _, fn := range obj.Functions {
		inst, err := s.canonicalFunction(ctx, dag, fn)
		if err != nil {
			return nil, fmt.Errorf("canonical object function %q: %w", fn.Name, err)
		}
		arr = append(arr, inst)
	}
	return arr, nil
}

func (s *moduleSchema) objectTypeDefConstructor(
	ctx context.Context,
	obj *core.ObjectTypeDef,
	_ struct{},
) (dagql.Nullable[dagql.ObjectResult[*core.Function]], error) {
	if !obj.Constructor.Valid {
		return dagql.Null[dagql.ObjectResult[*core.Function]](), nil
	}
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.Null[dagql.ObjectResult[*core.Function]](), fmt.Errorf("failed to get dag server: %w", err)
	}
	inst, err := s.canonicalFunction(ctx, dag, obj.Constructor.Value)
	if err != nil {
		return dagql.Null[dagql.ObjectResult[*core.Function]](), fmt.Errorf("canonical object constructor: %w", err)
	}
	return dagql.NonNull(inst), nil
}

func (s *moduleSchema) interfaceTypeDefFunctions(
	ctx context.Context,
	iface *core.InterfaceTypeDef,
	_ struct{},
) (dagql.ObjectResultArray[*core.Function], error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}
	arr := make(dagql.ObjectResultArray[*core.Function], 0, len(iface.Functions))
	for _, fn := range iface.Functions {
		inst, err := s.canonicalFunction(ctx, dag, fn)
		if err != nil {
			return nil, fmt.Errorf("canonical interface function %q: %w", fn.Name, err)
		}
		arr = append(arr, inst)
	}
	return arr, nil
}

func (s *moduleSchema) listElementTypeDef(
	ctx context.Context,
	list *core.ListTypeDef,
	_ struct{},
) (dagql.ObjectResult[*core.TypeDef], error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.TypeDef]{}, fmt.Errorf("failed to get dag server: %w", err)
	}
	return s.canonicalTypeDefRef(ctx, dag, list.ElementTypeDef)
}

func (s *moduleSchema) inputTypeDefFields(
	ctx context.Context,
	input *core.InputTypeDef,
	_ struct{},
) (dagql.ObjectResultArray[*core.FieldTypeDef], error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}
	return currentCallObjectResultArray(ctx, dag, input.Fields)
}

func (s *moduleSchema) enumTypeDefMembers(
	ctx context.Context,
	enum *core.EnumTypeDef,
	_ struct{},
) (dagql.ObjectResultArray[*core.EnumMemberTypeDef], error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}
	return currentCallObjectResultArray(ctx, dag, enum.Members)
}

func (s *moduleSchema) enumTypeDefValues(
	ctx context.Context,
	enum *core.EnumTypeDef,
	_ struct{},
) (dagql.ObjectResultArray[*core.EnumMemberTypeDef], error) {
	return s.enumTypeDefMembers(ctx, enum, struct{}{})
}

func (s *moduleSchema) canonicalCurrentTypeDefs(
	ctx context.Context,
	dag *dagql.Server,
	typeDefs []*core.TypeDef,
) (dagql.ObjectResultArray[*core.TypeDef], error) {
	arr := make(dagql.ObjectResultArray[*core.TypeDef], 0, len(typeDefs))
	seen := make(map[string]dagql.ObjectResult[*core.TypeDef], len(typeDefs))
	for _, typeDef := range typeDefs {
		key := canonicalTopLevelTypeDefKey(typeDef)
		inst, ok := seen[key]
		if ok {
			arr = append(arr, inst)
			continue
		}
		var err error
		inst, err = s.canonicalTopLevelTypeDef(ctx, dag, typeDef)
		if err != nil {
			return nil, err
		}
		seen[key] = inst
		arr = append(arr, inst)
	}
	return arr, nil
}

func (s *moduleSchema) canonicalTopLevelTypeDef(
	ctx context.Context,
	dag *dagql.Server,
	typeDef *core.TypeDef,
) (dagql.ObjectResult[*core.TypeDef], error) {
	if typeDef == nil {
		return dagql.ObjectResult[*core.TypeDef]{}, fmt.Errorf("canonical top-level typedef: nil typedef")
	}

	var inst dagql.ObjectResult[*core.TypeDef]
	switch typeDef.Kind {
	case core.TypeDefKindObject:
		if !typeDef.AsObject.Valid {
			return inst, fmt.Errorf("canonical top-level typedef: object kind missing object payload")
		}
		obj := typeDef.AsObject.Value
		args := []dagql.NamedInput{{Name: "name", Value: dagql.String(firstNonEmpty(obj.OriginalName, obj.Name))}}
		if obj.Description != "" {
			args = append(args, dagql.NamedInput{Name: "description", Value: dagql.String(obj.Description)})
		}
		if obj.SourceModuleName != "" {
			args = append(args, dagql.NamedInput{Name: "sourceModuleName", Value: dagql.Opt(dagql.String(obj.SourceModuleName))})
		}
		if obj.Deprecated != nil {
			args = append(args, dagql.NamedInput{Name: "deprecated", Value: dagql.Opt(dagql.String(*obj.Deprecated))})
		}
		args, err := s.appendSourceMapArg(ctx, dag, args, "sourceMap", obj.SourceMap)
		if err != nil {
			return inst, err
		}
		if err := dag.Select(ctx, dag.Root(), &inst,
			dagql.Selector{Field: "typeDef"},
			dagql.Selector{Field: "withObject", Args: args},
		); err != nil {
			return inst, fmt.Errorf("canonical top-level object typedef %q: %w", obj.Name, err)
		}
		for _, field := range obj.Fields {
			fieldType, err := s.canonicalTypeDefRef(ctx, dag, field.TypeDef)
			if err != nil {
				return inst, fmt.Errorf("canonical top-level object typedef %q field %q type: %w", obj.Name, field.Name, err)
			}
			fieldTypeID, err := fieldType.ID()
			if err != nil {
				return inst, fmt.Errorf("canonical top-level object typedef %q field %q id: %w", obj.Name, field.Name, err)
			}
			fieldArgs := []dagql.NamedInput{
				{Name: "name", Value: dagql.String(firstNonEmpty(field.OriginalName, field.Name))},
				{Name: "typeDef", Value: dagql.NewID[*core.TypeDef](fieldTypeID)},
			}
			if field.Description != "" {
				fieldArgs = append(fieldArgs, dagql.NamedInput{Name: "description", Value: dagql.String(field.Description)})
			}
			if field.Deprecated != nil {
				fieldArgs = append(fieldArgs, dagql.NamedInput{Name: "deprecated", Value: dagql.Opt(dagql.String(*field.Deprecated))})
			}
			fieldArgs, err = s.appendSourceMapArg(ctx, dag, fieldArgs, "sourceMap", field.SourceMap)
			if err != nil {
				return inst, err
			}
			if err := dag.Select(ctx, inst, &inst, dagql.Selector{Field: "withField", Args: fieldArgs}); err != nil {
				return inst, fmt.Errorf("canonical top-level object typedef %q field %q: %w", obj.Name, field.Name, err)
			}
		}
		for _, fn := range obj.Functions {
			fnInst, err := s.canonicalFunction(ctx, dag, fn)
			if err != nil {
				return inst, fmt.Errorf("canonical top-level object typedef %q function %q: %w", obj.Name, fn.Name, err)
			}
			fnID, err := fnInst.ID()
			if err != nil {
				return inst, fmt.Errorf("canonical top-level object typedef %q function %q id: %w", obj.Name, fn.Name, err)
			}
			if err := dag.Select(ctx, inst, &inst,
				dagql.Selector{
					Field: "withFunction",
					Args: []dagql.NamedInput{
						{Name: "function", Value: dagql.NewID[*core.Function](fnID)},
					},
				},
			); err != nil {
				return inst, fmt.Errorf("canonical top-level object typedef %q add function %q: %w", obj.Name, fn.Name, err)
			}
		}
		if obj.Constructor.Valid {
			fnInst, err := s.canonicalFunction(ctx, dag, obj.Constructor.Value)
			if err != nil {
				return inst, fmt.Errorf("canonical top-level object typedef %q constructor: %w", obj.Name, err)
			}
			fnID, err := fnInst.ID()
			if err != nil {
				return inst, fmt.Errorf("canonical top-level object typedef %q constructor id: %w", obj.Name, err)
			}
			if err := dag.Select(ctx, inst, &inst,
				dagql.Selector{
					Field: "withConstructor",
					Args: []dagql.NamedInput{
						{Name: "function", Value: dagql.NewID[*core.Function](fnID)},
					},
				},
			); err != nil {
				return inst, fmt.Errorf("canonical top-level object typedef %q add constructor: %w", obj.Name, err)
			}
		}
	case core.TypeDefKindInterface:
		if !typeDef.AsInterface.Valid {
			return inst, fmt.Errorf("canonical top-level typedef: interface kind missing interface payload")
		}
		iface := typeDef.AsInterface.Value
		args := []dagql.NamedInput{{Name: "name", Value: dagql.String(firstNonEmpty(iface.OriginalName, iface.Name))}}
		if iface.Description != "" {
			args = append(args, dagql.NamedInput{Name: "description", Value: dagql.String(iface.Description)})
		}
		if iface.SourceModuleName != "" {
			args = append(args, dagql.NamedInput{Name: "sourceModuleName", Value: dagql.Opt(dagql.String(iface.SourceModuleName))})
		}
		var err error
		args, err = s.appendSourceMapArg(ctx, dag, args, "sourceMap", iface.SourceMap)
		if err != nil {
			return inst, err
		}
		if err := dag.Select(ctx, dag.Root(), &inst,
			dagql.Selector{Field: "typeDef"},
			dagql.Selector{Field: "withInterface", Args: args},
		); err != nil {
			return inst, fmt.Errorf("canonical top-level interface typedef %q: %w", iface.Name, err)
		}
		for _, fn := range iface.Functions {
			fnInst, err := s.canonicalFunction(ctx, dag, fn)
			if err != nil {
				return inst, fmt.Errorf("canonical top-level interface typedef %q function %q: %w", iface.Name, fn.Name, err)
			}
			fnID, err := fnInst.ID()
			if err != nil {
				return inst, fmt.Errorf("canonical top-level interface typedef %q function %q id: %w", iface.Name, fn.Name, err)
			}
			if err := dag.Select(ctx, inst, &inst,
				dagql.Selector{
					Field: "withFunction",
					Args: []dagql.NamedInput{
						{Name: "function", Value: dagql.NewID[*core.Function](fnID)},
					},
				},
			); err != nil {
				return inst, fmt.Errorf("canonical top-level interface typedef %q add function %q: %w", iface.Name, fn.Name, err)
			}
		}
	case core.TypeDefKindEnum:
		if !typeDef.AsEnum.Valid {
			return inst, fmt.Errorf("canonical top-level typedef: enum kind missing enum payload")
		}
		enum := typeDef.AsEnum.Value
		args := []dagql.NamedInput{{Name: "name", Value: dagql.String(firstNonEmpty(enum.OriginalName, enum.Name))}}
		if enum.Description != "" {
			args = append(args, dagql.NamedInput{Name: "description", Value: dagql.String(enum.Description)})
		}
		if enum.SourceModuleName != "" {
			args = append(args, dagql.NamedInput{Name: "sourceModuleName", Value: dagql.Opt(dagql.String(enum.SourceModuleName))})
		}
		var err error
		args, err = s.appendSourceMapArg(ctx, dag, args, "sourceMap", enum.SourceMap)
		if err != nil {
			return inst, err
		}
		if err := dag.Select(ctx, dag.Root(), &inst,
			dagql.Selector{Field: "typeDef"},
			dagql.Selector{Field: "withEnum", Args: args},
		); err != nil {
			return inst, fmt.Errorf("canonical top-level enum typedef %q: %w", enum.Name, err)
		}
		modernValues := make(map[string]struct{}, len(enum.Members))
		for _, member := range enum.Members {
			if member.Value != "" {
				modernValues[member.Value] = struct{}{}
			}
		}
		seenMemberNames := make(map[string]struct{}, len(enum.Members))
		seenMemberValues := make(map[string]struct{}, len(enum.Members))
		for _, member := range enum.Members {
			memberName := firstNonEmpty(member.OriginalName, member.Name)
			if member.Value == "" {
				if _, hasModern := modernValues[memberName]; hasModern {
					continue
				}
			}
			if _, seen := seenMemberNames[memberName]; seen {
				continue
			}
			if member.Value != "" {
				if _, seen := seenMemberValues[member.Value]; seen {
					continue
				}
			}
			memberArgs := []dagql.NamedInput{
				{Name: "name", Value: dagql.String(memberName)},
			}
			if member.Value != "" {
				memberArgs = append(memberArgs, dagql.NamedInput{Name: "value", Value: dagql.String(member.Value)})
			}
			if member.Description != "" {
				memberArgs = append(memberArgs, dagql.NamedInput{Name: "description", Value: dagql.String(member.Description)})
			}
			if member.Deprecated != nil {
				memberArgs = append(memberArgs, dagql.NamedInput{Name: "deprecated", Value: dagql.Opt(dagql.String(*member.Deprecated))})
			}
			memberArgs, err = s.appendSourceMapArg(ctx, dag, memberArgs, "sourceMap", member.SourceMap)
			if err != nil {
				return inst, err
			}
			if err := dag.Select(ctx, inst, &inst, dagql.Selector{Field: "withEnumMember", Args: memberArgs}); err != nil {
				return inst, fmt.Errorf("canonical top-level enum typedef %q member %q: %w", enum.Name, member.Name, err)
			}
			seenMemberNames[memberName] = struct{}{}
			if member.Value != "" {
				seenMemberValues[member.Value] = struct{}{}
			}
		}
	case core.TypeDefKindScalar:
		if !typeDef.AsScalar.Valid {
			return inst, fmt.Errorf("canonical top-level typedef: scalar kind missing scalar payload")
		}
		scalar := typeDef.AsScalar.Value
		args := []dagql.NamedInput{{Name: "name", Value: dagql.String(firstNonEmpty(scalar.OriginalName, scalar.Name))}}
		if scalar.Description != "" {
			args = append(args, dagql.NamedInput{Name: "description", Value: dagql.String(scalar.Description)})
		}
		if scalar.SourceModuleName != "" {
			args = append(args, dagql.NamedInput{Name: "sourceModuleName", Value: dagql.Opt(dagql.String(scalar.SourceModuleName))})
		}
		if err := dag.Select(ctx, dag.Root(), &inst,
			dagql.Selector{Field: "typeDef"},
			dagql.Selector{Field: "withScalar", Args: args},
		); err != nil {
			return inst, fmt.Errorf("canonical top-level scalar typedef %q: %w", scalar.Name, err)
		}
	case core.TypeDefKindInput:
		if !typeDef.AsInput.Valid {
			return inst, fmt.Errorf("canonical top-level typedef: input kind missing input payload")
		}
		if err := dag.Select(ctx, dag.Root(), &inst,
			dagql.Selector{
				Field: "__inputTypeDef",
				Args: []dagql.NamedInput{
					{Name: "name", Value: dagql.String(typeDef.AsInput.Value.Name)},
				},
			},
		); err != nil {
			return inst, fmt.Errorf("canonical top-level input typedef %q: %w", typeDef.AsInput.Value.Name, err)
		}
	default:
		var err error
		inst, err = s.canonicalTypeDefRef(ctx, dag, typeDef)
		if err != nil {
			return inst, err
		}
	}
	if typeDef.Optional {
		return s.withOptional(ctx, dag, inst)
	}
	return inst, nil
}

func (s *moduleSchema) canonicalTypeDefRef(
	ctx context.Context,
	dag *dagql.Server,
	typeDef *core.TypeDef,
) (dagql.ObjectResult[*core.TypeDef], error) {
	if typeDef == nil {
		return dagql.ObjectResult[*core.TypeDef]{}, fmt.Errorf("canonical typedef ref: nil typedef")
	}

	var inst dagql.ObjectResult[*core.TypeDef]
	switch typeDef.Kind {
	case core.TypeDefKindString, core.TypeDefKindInteger, core.TypeDefKindFloat, core.TypeDefKindBoolean, core.TypeDefKindVoid:
		if err := dag.Select(ctx, dag.Root(), &inst,
			dagql.Selector{Field: "typeDef"},
			dagql.Selector{
				Field: "withKind",
				Args: []dagql.NamedInput{
					{Name: "kind", Value: typeDef.Kind},
				},
			},
		); err != nil {
			return inst, fmt.Errorf("canonical typedef ref kind %q: %w", typeDef.Kind, err)
		}
	case core.TypeDefKindList:
		if !typeDef.AsList.Valid {
			return inst, fmt.Errorf("canonical typedef ref: list kind missing list payload")
		}
		elemInst, err := s.canonicalTypeDefRef(ctx, dag, typeDef.AsList.Value.ElementTypeDef)
		if err != nil {
			return inst, fmt.Errorf("canonical typedef ref list element: %w", err)
		}
		elemID, err := elemInst.ID()
		if err != nil {
			return inst, fmt.Errorf("canonical typedef ref list element id: %w", err)
		}
		if err := dag.Select(ctx, dag.Root(), &inst,
			dagql.Selector{Field: "typeDef"},
			dagql.Selector{
				Field: "withListOf",
				Args: []dagql.NamedInput{
					{Name: "elementType", Value: dagql.NewID[*core.TypeDef](elemID)},
				},
			},
		); err != nil {
			return inst, fmt.Errorf("canonical typedef ref list: %w", err)
		}
	case core.TypeDefKindObject:
		if !typeDef.AsObject.Valid {
			return inst, fmt.Errorf("canonical typedef ref: object kind missing object payload")
		}
		if err := dag.Select(ctx, dag.Root(), &inst,
			dagql.Selector{Field: "typeDef"},
			dagql.Selector{
				Field: "withObject",
				Args: []dagql.NamedInput{
					{Name: "name", Value: dagql.String(firstNonEmpty(typeDef.AsObject.Value.OriginalName, typeDef.AsObject.Value.Name))},
					{Name: "sourceModuleName", Value: dagql.Opt(dagql.String(typeDef.AsObject.Value.SourceModuleName))},
				},
			},
		); err != nil {
			return inst, fmt.Errorf("canonical typedef ref object %q: %w", typeDef.AsObject.Value.Name, err)
		}
	case core.TypeDefKindInterface:
		if !typeDef.AsInterface.Valid {
			return inst, fmt.Errorf("canonical typedef ref: interface kind missing interface payload")
		}
		if err := dag.Select(ctx, dag.Root(), &inst,
			dagql.Selector{Field: "typeDef"},
			dagql.Selector{
				Field: "withInterface",
				Args: []dagql.NamedInput{
					{Name: "name", Value: dagql.String(firstNonEmpty(typeDef.AsInterface.Value.OriginalName, typeDef.AsInterface.Value.Name))},
					{Name: "sourceModuleName", Value: dagql.Opt(dagql.String(typeDef.AsInterface.Value.SourceModuleName))},
				},
			},
		); err != nil {
			return inst, fmt.Errorf("canonical typedef ref interface %q: %w", typeDef.AsInterface.Value.Name, err)
		}
	case core.TypeDefKindScalar:
		if !typeDef.AsScalar.Valid {
			return inst, fmt.Errorf("canonical typedef ref: scalar kind missing scalar payload")
		}
		if err := dag.Select(ctx, dag.Root(), &inst,
			dagql.Selector{Field: "typeDef"},
			dagql.Selector{
				Field: "withScalar",
				Args: []dagql.NamedInput{
					{Name: "name", Value: dagql.String(firstNonEmpty(typeDef.AsScalar.Value.OriginalName, typeDef.AsScalar.Value.Name))},
					{Name: "sourceModuleName", Value: dagql.Opt(dagql.String(typeDef.AsScalar.Value.SourceModuleName))},
				},
			},
		); err != nil {
			return inst, fmt.Errorf("canonical typedef ref scalar %q: %w", typeDef.AsScalar.Value.Name, err)
		}
	case core.TypeDefKindEnum:
		if !typeDef.AsEnum.Valid {
			return inst, fmt.Errorf("canonical typedef ref: enum kind missing enum payload")
		}
		if err := dag.Select(ctx, dag.Root(), &inst,
			dagql.Selector{Field: "typeDef"},
			dagql.Selector{
				Field: "withEnum",
				Args: []dagql.NamedInput{
					{Name: "name", Value: dagql.String(firstNonEmpty(typeDef.AsEnum.Value.OriginalName, typeDef.AsEnum.Value.Name))},
					{Name: "sourceModuleName", Value: dagql.Opt(dagql.String(typeDef.AsEnum.Value.SourceModuleName))},
				},
			},
		); err != nil {
			return inst, fmt.Errorf("canonical typedef ref enum %q: %w", typeDef.AsEnum.Value.Name, err)
		}
	case core.TypeDefKindInput:
		if !typeDef.AsInput.Valid {
			return inst, fmt.Errorf("canonical typedef ref: input kind missing input payload")
		}
		if err := dag.Select(ctx, dag.Root(), &inst,
			dagql.Selector{
				Field: "__inputTypeDef",
				Args: []dagql.NamedInput{
					{Name: "name", Value: dagql.String(typeDef.AsInput.Value.Name)},
				},
			},
		); err != nil {
			return inst, fmt.Errorf("canonical typedef ref input %q: %w", typeDef.AsInput.Value.Name, err)
		}
	default:
		return inst, fmt.Errorf("canonical typedef ref: unsupported kind %q", typeDef.Kind)
	}
	if typeDef.Optional {
		return s.withOptional(ctx, dag, inst)
	}
	return inst, nil
}

func (s *moduleSchema) canonicalFunction(
	ctx context.Context,
	dag *dagql.Server,
	fn *core.Function,
) (dagql.ObjectResult[*core.Function], error) {
	if fn == nil {
		return dagql.ObjectResult[*core.Function]{}, fmt.Errorf("canonical function: nil function")
	}

	returnType, err := s.canonicalTypeDefRef(ctx, dag, fn.ReturnType)
	if err != nil {
		return dagql.ObjectResult[*core.Function]{}, fmt.Errorf("canonical function %q return type: %w", fn.Name, err)
	}
	returnTypeID, err := returnType.ID()
	if err != nil {
		return dagql.ObjectResult[*core.Function]{}, fmt.Errorf("canonical function %q return type id: %w", fn.Name, err)
	}

	var inst dagql.ObjectResult[*core.Function]
	if err := dag.Select(ctx, dag.Root(), &inst,
		dagql.Selector{
			Field: "function",
			Args: []dagql.NamedInput{
				{Name: "name", Value: dagql.String(firstNonEmpty(fn.OriginalName, fn.Name))},
				{Name: "returnType", Value: dagql.NewID[*core.TypeDef](returnTypeID)},
			},
		},
	); err != nil {
		return inst, fmt.Errorf("canonical function %q: %w", fn.Name, err)
	}

	if fn.Description != "" {
		if err := dag.Select(ctx, inst, &inst,
			dagql.Selector{
				Field: "withDescription",
				Args: []dagql.NamedInput{
					{Name: "description", Value: dagql.String(fn.Description)},
				},
			},
		); err != nil {
			return inst, fmt.Errorf("canonical function %q description: %w", fn.Name, err)
		}
	}
	if fn.Deprecated != nil {
		if err := dag.Select(ctx, inst, &inst,
			dagql.Selector{
				Field: "withDeprecated",
				Args: []dagql.NamedInput{
					{Name: "reason", Value: dagql.Opt(dagql.String(*fn.Deprecated))},
				},
			},
		); err != nil {
			return inst, fmt.Errorf("canonical function %q deprecated: %w", fn.Name, err)
		}
	}
	if fn.IsCheck {
		if err := dag.Select(ctx, inst, &inst, dagql.Selector{Field: "withCheck"}); err != nil {
			return inst, fmt.Errorf("canonical function %q check: %w", fn.Name, err)
		}
	}
	if fn.IsGenerator {
		if err := dag.Select(ctx, inst, &inst, dagql.Selector{Field: "withGenerator"}); err != nil {
			return inst, fmt.Errorf("canonical function %q generator: %w", fn.Name, err)
		}
	}
	if fn.SourceMap.Valid {
		sourceMap, err := s.canonicalSourceMap(ctx, dag, fn.SourceMap.Value)
		if err != nil {
			return inst, fmt.Errorf("canonical function %q source map: %w", fn.Name, err)
		}
		sourceMapID, err := sourceMap.ID()
		if err != nil {
			return inst, fmt.Errorf("canonical function %q source map id: %w", fn.Name, err)
		}
		if err := dag.Select(ctx, inst, &inst,
			dagql.Selector{
				Field: "withSourceMap",
				Args: []dagql.NamedInput{
					{Name: "sourceMap", Value: dagql.NewID[*core.SourceMap](sourceMapID)},
				},
			},
		); err != nil {
			return inst, fmt.Errorf("canonical function %q with source map: %w", fn.Name, err)
		}
	}
	if fn.CachePolicy != "" || fn.CacheTTLSeconds.Valid {
		cacheArgs := []dagql.NamedInput{{Name: "policy", Value: fn.CachePolicy}}
		if fn.CacheTTLSeconds.Valid {
			cacheArgs = append(cacheArgs, dagql.NamedInput{
				Name:  "timeToLive",
				Value: dagql.Opt(dagql.String((time.Duration(fn.CacheTTLSeconds.Value) * time.Second).String())),
			})
		}
		if err := dag.Select(ctx, inst, &inst, dagql.Selector{Field: "withCachePolicy", Args: cacheArgs}); err != nil {
			return inst, fmt.Errorf("canonical function %q cache policy: %w", fn.Name, err)
		}
	}
	for _, arg := range fn.Args {
		argType, err := s.canonicalTypeDefRef(ctx, dag, arg.TypeDef)
		if err != nil {
			return inst, fmt.Errorf("canonical function %q arg %q type: %w", fn.Name, arg.Name, err)
		}
		argTypeID, err := argType.ID()
		if err != nil {
			return inst, fmt.Errorf("canonical function %q arg %q type id: %w", fn.Name, arg.Name, err)
		}
		argArgs := []dagql.NamedInput{
			{Name: "name", Value: dagql.String(firstNonEmpty(arg.OriginalName, arg.Name))},
			{Name: "typeDef", Value: dagql.NewID[*core.TypeDef](argTypeID)},
		}
		if arg.Description != "" {
			argArgs = append(argArgs, dagql.NamedInput{Name: "description", Value: dagql.String(arg.Description)})
		}
		if arg.DefaultValue != nil {
			argArgs = append(argArgs, dagql.NamedInput{Name: "defaultValue", Value: arg.DefaultValue})
		}
		if arg.DefaultPath != "" {
			argArgs = append(argArgs, dagql.NamedInput{Name: "defaultPath", Value: dagql.String(arg.DefaultPath)})
		}
		if arg.DefaultAddress != "" {
			argArgs = append(argArgs, dagql.NamedInput{Name: "defaultAddress", Value: dagql.String(arg.DefaultAddress)})
		}
		if len(arg.Ignore) > 0 {
			argArgs = append(argArgs, dagql.NamedInput{
				Name:  "ignore",
				Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(arg.Ignore...)),
			})
		}
		if arg.Deprecated != nil {
			argArgs = append(argArgs, dagql.NamedInput{Name: "deprecated", Value: dagql.Opt(dagql.String(*arg.Deprecated))})
		}
		argArgs, err = s.appendSourceMapArg(ctx, dag, argArgs, "sourceMap", arg.SourceMap)
		if err != nil {
			return inst, err
		}
		if err := dag.Select(ctx, inst, &inst, dagql.Selector{Field: "withArg", Args: argArgs}); err != nil {
			return inst, fmt.Errorf("canonical function %q arg %q: %w", fn.Name, arg.Name, err)
		}
	}
	return inst, nil
}

func (s *moduleSchema) canonicalSourceMap(
	ctx context.Context,
	dag *dagql.Server,
	sourceMap *core.SourceMap,
) (dagql.ObjectResult[*core.SourceMap], error) {
	var inst dagql.ObjectResult[*core.SourceMap]
	if sourceMap == nil {
		return inst, fmt.Errorf("canonical source map: nil source map")
	}
	if err := dag.Select(ctx, dag.Root(), &inst,
		dagql.Selector{
			Field: "sourceMap",
			Args: []dagql.NamedInput{
				{Name: "module", Value: dagql.Opt(dagql.String(sourceMap.Module))},
				{Name: "filename", Value: dagql.String(sourceMap.Filename)},
				{Name: "line", Value: dagql.Int(sourceMap.Line)},
				{Name: "column", Value: dagql.Int(sourceMap.Column)},
				{Name: "url", Value: dagql.Opt(dagql.String(sourceMap.URL))},
			},
		},
	); err != nil {
		return inst, fmt.Errorf("canonical source map %q:%d:%d: %w", sourceMap.Filename, sourceMap.Line, sourceMap.Column, err)
	}
	return inst, nil
}

func (s *moduleSchema) appendSourceMapArg(
	ctx context.Context,
	dag *dagql.Server,
	args []dagql.NamedInput,
	argName string,
	sourceMap dagql.Nullable[*core.SourceMap],
) ([]dagql.NamedInput, error) {
	if !sourceMap.Valid {
		return args, nil
	}
	inst, err := s.canonicalSourceMap(ctx, dag, sourceMap.Value)
	if err != nil {
		return nil, err
	}
	id, err := inst.ID()
	if err != nil {
		return nil, fmt.Errorf("canonical source map id: %w", err)
	}
	return append(args, dagql.NamedInput{
		Name:  argName,
		Value: dagql.Opt(dagql.NewID[*core.SourceMap](id)),
	}), nil
}

func (s *moduleSchema) withOptional(
	ctx context.Context,
	dag *dagql.Server,
	inst dagql.ObjectResult[*core.TypeDef],
) (dagql.ObjectResult[*core.TypeDef], error) {
	if err := dag.Select(ctx, inst, &inst,
		dagql.Selector{
			Field: "withOptional",
			Args: []dagql.NamedInput{
				{Name: "optional", Value: dagql.Boolean(true)},
			},
		},
	); err != nil {
		return inst, fmt.Errorf("canonical typedef optional wrapper: %w", err)
	}
	return inst, nil
}

func firstNonEmpty(vals ...string) string {
	for _, val := range vals {
		if val != "" {
			return val
		}
	}
	return ""
}

func canonicalTopLevelTypeDefKey(typeDef *core.TypeDef) string {
	if typeDef == nil {
		return "<nil>"
	}
	switch typeDef.Kind {
	case core.TypeDefKindObject:
		if typeDef.AsObject.Valid {
			return fmt.Sprintf("object:%t:%s", typeDef.Optional, typeDef.AsObject.Value.Name)
		}
	case core.TypeDefKindInterface:
		if typeDef.AsInterface.Valid {
			return fmt.Sprintf("interface:%t:%s", typeDef.Optional, typeDef.AsInterface.Value.Name)
		}
	case core.TypeDefKindInput:
		if typeDef.AsInput.Valid {
			return fmt.Sprintf("input:%t:%s", typeDef.Optional, typeDef.AsInput.Value.Name)
		}
	case core.TypeDefKindScalar:
		if typeDef.AsScalar.Valid {
			return fmt.Sprintf("scalar:%t:%s", typeDef.Optional, typeDef.AsScalar.Value.Name)
		}
	case core.TypeDefKindEnum:
		if typeDef.AsEnum.Valid {
			return fmt.Sprintf("enum:%t:%s", typeDef.Optional, typeDef.AsEnum.Value.Name)
		}
	case core.TypeDefKindList:
		if typeDef.AsList.Valid && typeDef.AsList.Value.ElementTypeDef != nil {
			return fmt.Sprintf("list:%t:%s", typeDef.Optional, canonicalTopLevelTypeDefKey(typeDef.AsList.Value.ElementTypeDef))
		}
	default:
		return fmt.Sprintf("kind:%t:%s", typeDef.Optional, typeDef.Kind)
	}
	return fmt.Sprintf("fallback:%t:%s", typeDef.Optional, typeDef.Kind)
}

func currentCallNullableObjectResult[T dagql.Typed](
	ctx context.Context,
	val dagql.Nullable[T],
) (dagql.Nullable[dagql.ObjectResult[T]], error) {
	if !val.Valid {
		return dagql.Null[dagql.ObjectResult[T]](), nil
	}
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.Null[dagql.ObjectResult[T]](), fmt.Errorf("failed to get dag server: %w", err)
	}
	inst, err := dagql.NewObjectResultForCurrentCall(ctx, dag, val.Value)
	if err != nil {
		return dagql.Null[dagql.ObjectResult[T]](), err
	}
	return dagql.NonNull(inst), nil
}

func currentCallObjectResultArray[T dagql.Typed](
	ctx context.Context,
	dag *dagql.Server,
	items []T,
) (dagql.ObjectResultArray[T], error) {
	arrResult, err := dagql.NewResultForCurrentCall(ctx, dagql.Array[T](items))
	if err != nil {
		return nil, fmt.Errorf("wrap current call array: %w", err)
	}
	arr := make(dagql.ObjectResultArray[T], 0, len(items))
	for i, item := range items {
		nth, err := arrResult.NthValue(ctx, i+1)
		if err != nil {
			return nil, fmt.Errorf("current call array nth %d: %w", i+1, err)
		}
		nthCall, err := nth.ResultCall()
		if err != nil {
			return nil, fmt.Errorf("current call array nth %d result call: %w", i+1, err)
		}
		inst, err := dagql.NewObjectResultForCall(item, dag, nthCall)
		if err != nil {
			return nil, fmt.Errorf("current call array nth %d object result: %w", i+1, err)
		}
		arr = append(arr, inst)
	}
	return arr, nil
}
