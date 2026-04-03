package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/buildkit"
	telemetry "github.com/dagger/otel-go"
	"github.com/vito/dang/pkg/dang"
	"github.com/vito/dang/pkg/hm"
	"github.com/vito/dang/pkg/introspection"
	"github.com/vito/dang/pkg/ioctx"
	"go.opentelemetry.io/otel/propagation"
)

func (r *DangRuntime) eval(
	ctx context.Context,
	query *core.Query,
	schemaFile dagql.Result[*core.File],
	execMD *buildkit.ExecutionMetadata,
	fnCall *core.FunctionCall,
) ([]byte, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}
	defer l.Close()

	httpSrv := &http.Server{
		ReadHeaderTimeout: 10 * time.Second,
		Handler: http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
			telemetry.Propagator.Inject(ctx, propagation.HeaderCarrier(req.Header))
			query.ServeHTTPToNestedClient(resp, req, execMD)
		}),
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer shutdownCancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	}()

	srvErrCh := make(chan error, 1)
	go func() {
		err := httpSrv.Serve(l)
		if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			srvErrCh <- err
		}
		close(srvErrCh)
	}()

	gqlClient := graphql.NewClient(fmt.Sprintf("http://%s/query", l.Addr()), nil)

	var intro introspection.Response
	f, err := schemaFile.Self().Open(ctx)
	if err != nil {
		return nil, fmt.Errorf("open schema file: %w", err)
	}
	defer f.Close()
	if err := json.NewDecoder(f).Decode(&intro); err != nil {
		return nil, fmt.Errorf("decode schema: %w", err)
	}

	ctx = dang.ContextWithImportConfigs(ctx, dang.ImportConfig{
		Name:       "Dagger",
		Client:     gqlClient,
		Schema:     intro.Schema,
		AutoImport: true,
	})

	stdio := telemetry.SpanStdio(ctx, core.InstrumentationLibrary)
	ctx = ioctx.StdoutToContext(ctx, stdio.Stdout)
	ctx = ioctx.StderrToContext(ctx, stdio.Stderr)

	modCtx := r.modSource.Self().ContextDirectory
	var env dang.EvalEnv
	err = modCtx.Self().Mount(ctx, func(path string) error {
		modSrcDir := filepath.Join(path, r.modSource.Self().SourceSubpath)
		env, err = dang.RunDir(ctx, modSrcDir, false)
		if err != nil {
			return fmt.Errorf("run dir: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("mount source: %w", err)
	}

	select {
	case serveErr, ok := <-srvErrCh:
		if ok && serveErr != nil {
			return nil, fmt.Errorf("serve nested client: %w", serveErr)
		}
	default:
	}

	if fnCall.ParentName == "" {
		srv, err := core.CurrentDagqlServer(ctx)
		if err != nil {
			return nil, fmt.Errorf("get dagql server: %w", err)
		}
		dagMod, err := initDangModule(ctx, srv, env)
		if err != nil {
			return nil, fmt.Errorf("init module: %w", err)
		}
		return json.Marshal(dagMod)
	}

	result, err := callDangFunction(ctx, env, fnCall)
	if err != nil {
		return nil, err
	}

	return json.Marshal(result)
}

func callDangFunction(ctx context.Context, env dang.EvalEnv, fnCall *core.FunctionCall) (dang.Value, error) {
	inputArgs := make(map[string][]byte, len(fnCall.InputArgs))
	for _, arg := range fnCall.InputArgs {
		inputArgs[arg.Name] = []byte(arg.Value)
	}

	parentModBase, found := env.Get(fnCall.ParentName)
	if !found {
		return nil, fmt.Errorf("unknown parent type: %s", fnCall.ParentName)
	}

	var parentState map[string]any
	dec := json.NewDecoder(bytes.NewReader(fnCall.Parent))
	dec.UseNumber()
	if err := dec.Decode(&parentState); err != nil {
		return nil, fmt.Errorf("unmarshal parent: %w", err)
	}

	parentConstructor := parentModBase.(*dang.ConstructorFunction)
	parentModType := parentConstructor.ClassType

	var fnType *hm.FunctionType
	if fnCall.Name == "" {
		fnType = parentConstructor.FnType
	} else {
		fnScheme, found := parentModType.SchemeOf(fnCall.Name)
		if !found {
			return nil, fmt.Errorf("unknown function: %s", fnCall.Name)
		}
		t, mono := fnScheme.Type()
		if !mono {
			return nil, fmt.Errorf("non-monotype function %s", fnCall.Name)
		}
		var ok bool
		fnType, ok = t.(*hm.FunctionType)
		if !ok {
			return nil, fmt.Errorf("expected function type, got %T", fnScheme)
		}
	}

	var args dang.Record
	argMap := make(map[string]dang.Value, len(args))
	for _, arg := range fnType.Arg().(*dang.RecordType).Fields {
		argType, mono := arg.Value.Type()
		if !mono {
			return nil, fmt.Errorf("non-monotype argument %s", arg.Key)
		}
		jsonValue, provided := inputArgs[arg.Key]
		if !provided {
			continue
		}
		dec := json.NewDecoder(bytes.NewReader(jsonValue))
		dec.UseNumber()
		var val any
		if err := dec.Decode(&val); err != nil {
			return nil, fmt.Errorf("unmarshal arg %s: %w", arg.Key, err)
		}
		dangVal, err := anyToDang(ctx, env, val, argType)
		if err != nil {
			return nil, fmt.Errorf("convert arg %s: %w", arg.Key, err)
		}
		argMap[arg.Key] = dangVal
		args = append(args, dang.Keyed[dang.Node]{
			Key:   arg.Key,
			Value: &dang.ValueNode{Val: dangVal},
		})
	}

	if fnCall.Name == "" {
		return parentConstructor.Call(ctx, env, argMap)
	}

	parentModEnv := dang.NewModuleValue(parentModType)
	parentModEnv.SetDynamicScope(parentModEnv)

	for name, value := range parentState {
		scheme, found := parentModType.SchemeOf(name)
		if !found {
			return nil, fmt.Errorf("unknown field: %s", name)
		}
		fieldType, isMono := scheme.Type()
		if !isMono {
			return nil, fmt.Errorf("non-monotype field %s", name)
		}
		dangVal, err := anyToDang(ctx, env, value, fieldType)
		if err != nil {
			return nil, fmt.Errorf("convert field %s: %w", name, err)
		}
		parentModEnv.Set(name, dangVal)
	}

	bodyEnv := dang.CreateCompositeEnv(parentModEnv, env)
	_, err := dang.EvaluateFormsWithPhases(ctx, parentConstructor.ClassBodyForms, bodyEnv)
	if err != nil {
		return nil, fmt.Errorf("evaluating class body for %s: %w", parentConstructor.ClassName, err)
	}

	call := &dang.FunCall{
		Fun: &dang.Select{
			Receiver: &dang.ValueNode{Val: parentModEnv},
			Field:    &dang.Symbol{Name: fnCall.Name},
		},
		Args: args,
	}
	return call.Eval(ctx, env)
}

func initDangModule(ctx context.Context, srv *dagql.Server, env dang.EvalEnv) (res dagql.ObjectResult[*core.Module], _ error) {
	sels := []dagql.Selector{
		{
			Field: "module",
		},
	}

	if descBinding, found := env.Get("description"); found {
		sels = append(sels, dagql.Selector{
			Field: "withDescription",
			Args: []dagql.NamedInput{
				{
					Name:  "description",
					Value: dagql.String(descBinding.String()),
				},
			},
		})
	}

	binds := env.Bindings(dang.PublicVisibility)
	for _, binding := range binds {
		switch val := binding.Value.(type) {
		case *dang.ConstructorFunction:
			objDef, err := createObjectTypeDef(ctx, srv, binding.Key, val)
			if err != nil {
				return res, fmt.Errorf("failed to create object %s: %w", binding.Key, err)
			}
			directives := processedDirectives{}
			for _, slot := range val.Parameters {
				slotName := slot.Name.Name
				for _, dir := range slot.Directives {
					if directives[slotName] == nil {
						directives[slotName] = map[string]map[string]any{}
					}
					for _, arg := range dir.Args {
						if directives[slotName][dir.Name] == nil {
							directives[slotName][dir.Name] = map[string]any{}
						}
						val, err := evalConstantValue(arg.Value)
						if err != nil {
							return res, fmt.Errorf("failed to evaluate directive argument %s.%s.%s: %w", slotName, dir.Name, arg.Key, err)
						}
						directives[slotName][dir.Name][arg.Key] = val
					}
				}
			}
			fnDef, err := createFunction(ctx, srv, binding.Key, val.FnType, directives)
			if err != nil {
				return res, fmt.Errorf("failed to create constructor for %s: %w", binding.Key, err)
			}
			fnDefID, err := fnDef.ID()
			if err != nil {
				return res, fmt.Errorf("failed to get constructor ID for %s: %w", binding.Key, err)
			}

			var objDefWithCtor dagql.ObjectResult[*core.TypeDef]
			if err := srv.Select(ctx, objDef, &objDefWithCtor, dagql.Selector{
				Field: "withConstructor",
				Args:  []dagql.NamedInput{{Name: "function", Value: dagql.NewID[*core.Function](fnDefID)}},
			}); err != nil {
				return res, fmt.Errorf("failed to add constructor to object: %w", err)
			}
			objDefWithCtorID, err := objDefWithCtor.ID()
			if err != nil {
				return res, fmt.Errorf("failed to get object typedef ID for %s: %w", binding.Key, err)
			}

			sels = append(sels, dagql.Selector{
				Field: "withObject",
				Args:  []dagql.NamedInput{{Name: "object", Value: dagql.NewID[*core.TypeDef](objDefWithCtorID)}},
			})
		}
	}

	if err := srv.Select(ctx, srv.Root(), &res, sels...); err != nil {
		return res, fmt.Errorf("failed to select module: %w", err)
	}

	return res, nil
}

type processedDirectives = map[string]map[string]map[string]any

func createFunction(ctx context.Context, srv *dagql.Server, name string, fn *hm.FunctionType, directives processedDirectives) (dagql.ObjectResult[*core.Function], error) {
	var res dagql.ObjectResult[*core.Function]

	retTypeDef, err := dangTypeToTypeDef(ctx, srv, fn.Ret(false))
	if err != nil {
		return res, fmt.Errorf("failed to convert return type for %s: %w", fn, err)
	}
	retTypeDefID, err := retTypeDef.ID()
	if err != nil {
		return res, fmt.Errorf("failed to get return type ID for %s: %w", name, err)
	}

	sels := []dagql.Selector{
		{
			Field: "function",
			Args: []dagql.NamedInput{
				{Name: "name", Value: dagql.String(name)},
				{Name: "returnType", Value: dagql.NewID[*core.TypeDef](retTypeDefID)},
			},
		},
	}

	for _, arg := range fn.Arg().(*dang.RecordType).Fields {
		argType, mono := arg.Value.Type()
		if !mono {
			return res, fmt.Errorf("non-monotype argument %s", arg.Key)
		}
		typeDef, err := dangTypeToTypeDef(ctx, srv, argType)
		if err != nil {
			return res, fmt.Errorf("failed to convert argument type for %s: %w", arg.Key, err)
		}

		if _, isNonNull := argType.(hm.NonNullType); !isNonNull {
			var optTypeDef dagql.ObjectResult[*core.TypeDef]
			if err := srv.Select(ctx, typeDef, &optTypeDef, dagql.Selector{
				Field: "withOptional",
				Args:  []dagql.NamedInput{{Name: "optional", Value: dagql.Boolean(true)}},
			}); err != nil {
				return res, fmt.Errorf("failed to make argument optional: %w", err)
			}
			typeDef = optTypeDef
		}
		typeDefID, err := typeDef.ID()
		if err != nil {
			return res, fmt.Errorf("failed to get argument type ID for %s: %w", arg.Key, err)
		}

		argArgs := []dagql.NamedInput{
			{Name: "name", Value: dagql.String(arg.Key)},
			{Name: "typeDef", Value: dagql.NewID[*core.TypeDef](typeDefID)},
		}

		if argDirectives, hasDirectives := directives[arg.Key]; hasDirectives {
			if defaultPath, hasDefaultPath := argDirectives["defaultPath"]; hasDefaultPath {
				if path, ok := defaultPath["path"].(string); ok {
					argArgs = append(argArgs, dagql.NamedInput{Name: "defaultPath", Value: dagql.String(path)})
				}
			}
			if ignorePatterns, hasIgnorePatterns := argDirectives["ignorePatterns"]; hasIgnorePatterns {
				ignoreVal, hasIgnore := ignorePatterns["patterns"]
				if patterns, ok := ignoreVal.([]any); ok {
					var ignore []string
					for _, pattern := range patterns {
						str, ok := pattern.(string)
						if !ok {
							return res, fmt.Errorf("invalid ignore argument %s: %T (expected string)", arg.Key, pattern)
						}
						ignore = append(ignore, str)
					}
					if len(ignore) > 0 {
						argArgs = append(argArgs, dagql.NamedInput{Name: "ignore", Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(ignore...))})
					}
				} else if hasIgnore {
					return res, fmt.Errorf("invalid ignore directive for argument %s: %T (expected []any)", arg.Key, ignoreVal)
				}
			}
		}

		sels = append(sels, dagql.Selector{
			Field: "withArg",
			Args:  argArgs,
		})
	}

	if err := srv.Select(ctx, srv.Root(), &res, sels...); err != nil {
		return res, fmt.Errorf("failed to create function: %w", err)
	}

	return res, nil
}

func evalConstantValue(node dang.Node) (any, error) {
	switch n := node.(type) {
	case *dang.String:
		return n.Value, nil
	case *dang.Int:
		return n.Value, nil
	case *dang.Boolean:
		return n.Value, nil
	case *dang.List:
		var elements []any
		for _, elem := range n.Elements {
			evalElem, err := evalConstantValue(elem)
			if err != nil {
				return nil, fmt.Errorf("failed to evaluate list element: %w", err)
			}
			elements = append(elements, evalElem)
		}
		return elements, nil
	default:
		return nil, fmt.Errorf("unsupported directive argument type: %T", node)
	}
}

func createObjectTypeDef(ctx context.Context, srv *dagql.Server, name string, module *dang.ConstructorFunction) (dagql.ObjectResult[*core.TypeDef], error) {
	var res dagql.ObjectResult[*core.TypeDef]

	sels := []dagql.Selector{
		{Field: "typeDef"},
		{
			Field: "withObject",
			Args:  []dagql.NamedInput{{Name: "name", Value: dagql.String(name)}},
		},
	}

	for bindingName, scheme := range module.ClassType.Bindings(dang.PublicVisibility) {
		slotType, isMono := scheme.Type()
		if !isMono {
			return res, fmt.Errorf("non-monotype method %s", bindingName)
		}
		switch x := slotType.(type) {
		case *hm.FunctionType:
			fnDef, err := createFunction(ctx, srv, bindingName, x, nil)
			if err != nil {
				return res, fmt.Errorf("failed to create method %s for %s: %w", bindingName, name, err)
			}

			if desc, ok := module.ClassType.GetDocString(bindingName); ok {
				var descFnDef dagql.ObjectResult[*core.Function]
				if err := srv.Select(ctx, fnDef, &descFnDef, dagql.Selector{
					Field: "withDescription",
					Args:  []dagql.NamedInput{{Name: "description", Value: dagql.String(desc)}},
				}); err != nil {
					return res, fmt.Errorf("failed to add description to function: %w", err)
				}
				fnDef = descFnDef
			}
			fnDefID, err := fnDef.ID()
			if err != nil {
				return res, fmt.Errorf("failed to get function ID for %s: %w", bindingName, err)
			}

			sels = append(sels, dagql.Selector{
				Field: "withFunction",
				Args:  []dagql.NamedInput{{Name: "function", Value: dagql.NewID[*core.Function](fnDefID)}},
			})
		default:
			fieldDef, err := dangTypeToTypeDef(ctx, srv, slotType)
			if err != nil {
				return res, fmt.Errorf("failed to create field %s: %w", bindingName, err)
			}
			fieldDefID, err := fieldDef.ID()
			if err != nil {
				return res, fmt.Errorf("failed to get field type ID for %s: %w", bindingName, err)
			}

			fieldArgs := []dagql.NamedInput{
				{Name: "name", Value: dagql.String(bindingName)},
				{Name: "typeDef", Value: dagql.NewID[*core.TypeDef](fieldDefID)},
			}

			if desc, ok := module.ClassType.GetDocString(bindingName); ok {
				fieldArgs = append(fieldArgs, dagql.NamedInput{Name: "description", Value: dagql.String(desc)})
			}

			sels = append(sels, dagql.Selector{
				Field: "withField",
				Args:  fieldArgs,
			})
		}
	}

	if err := srv.Select(ctx, srv.Root(), &res, sels...); err != nil {
		return res, fmt.Errorf("failed to create object typedef: %w", err)
	}

	return res, nil
}

func dangTypeToTypeDef(ctx context.Context, srv *dagql.Server, dangType hm.Type) (dagql.ObjectResult[*core.TypeDef], error) {
	var res dagql.ObjectResult[*core.TypeDef]

	sels := []dagql.Selector{{Field: "typeDef"}}

	if nonNull, isNonNull := dangType.(hm.NonNullType); isNonNull {
		inner, err := dangTypeToTypeDef(ctx, srv, nonNull.Type)
		if err != nil {
			return res, fmt.Errorf("failed to convert non-null type: %w", err)
		}
		if err := srv.Select(ctx, inner, &res, dagql.Selector{
			Field: "withOptional",
			Args:  []dagql.NamedInput{{Name: "optional", Value: dagql.Boolean(false)}},
		}); err != nil {
			return res, err
		}
		return res, nil
	}

	sels = append(sels, dagql.Selector{
		Field: "withOptional",
		Args:  []dagql.NamedInput{{Name: "optional", Value: dagql.Boolean(true)}},
	})

	switch t := dangType.(type) {
	case dang.ListType:
		elemTypeDef, err := dangTypeToTypeDef(ctx, srv, t.Type)
		if err != nil {
			return res, fmt.Errorf("failed to convert list element type: %w", err)
		}
		elemTypeDefID, err := elemTypeDef.ID()
		if err != nil {
			return res, fmt.Errorf("failed to get list element type ID: %w", err)
		}
		sels = append(sels, dagql.Selector{
			Field: "withListOf",
			Args: []dagql.NamedInput{
				{Name: "elementType", Value: dagql.NewID[*core.TypeDef](elemTypeDefID)},
			},
		})
	case *dang.Module:
		switch t.Named {
		case "String":
			sels = append(sels, dagql.Selector{
				Field: "withKind",
				Args:  []dagql.NamedInput{{Name: "kind", Value: core.TypeDefKindString}},
			})
		case "Int":
			sels = append(sels, dagql.Selector{
				Field: "withKind",
				Args:  []dagql.NamedInput{{Name: "kind", Value: core.TypeDefKindInteger}},
			})
		case "Boolean":
			sels = append(sels, dagql.Selector{
				Field: "withKind",
				Args:  []dagql.NamedInput{{Name: "kind", Value: core.TypeDefKindBoolean}},
			})
		case "Void":
			sels = append(sels, dagql.Selector{
				Field: "withKind",
				Args:  []dagql.NamedInput{{Name: "kind", Value: core.TypeDefKindVoid}},
			})
		case "":
			return res, fmt.Errorf("cannot directly expose ad-hoc object type: %s", t)
		default:
			sels = append(sels, dagql.Selector{
				Field: "withObject",
				Args:  []dagql.NamedInput{{Name: "name", Value: dagql.String(t.Named)}},
			})
		}
	default:
		return res, fmt.Errorf("unknown type: %T: %s", dangType, dangType)
	}

	if err := srv.Select(ctx, srv.Root(), &res, sels...); err != nil {
		return res, fmt.Errorf("failed to select typedef: %w", err)
	}

	return res, nil
}

func anyToDang(ctx context.Context, env dang.EvalEnv, val any, fieldType hm.Type) (dang.Value, error) {
	if nonNull, ok := fieldType.(hm.NonNullType); ok {
		return anyToDang(ctx, env, val, nonNull.Type)
	}
	switch v := val.(type) {
	case string:
		if modType, ok := fieldType.(*dang.Module); ok && modType != dang.StringType {
			sel := &dang.FunCall{
				Fun: &dang.Select{
					Field: &dang.Symbol{Name: fmt.Sprintf("load%sFromID", modType.Named)},
				},
				Args: dang.Record{
					dang.Keyed[dang.Node]{
						Key:   "id",
						Value: &dang.String{Value: v},
					},
				},
			}
			return sel.Eval(ctx, env)
		}
		return dang.StringValue{Val: v}, nil
	case int:
		return dang.IntValue{Val: v}, nil
	case json.Number:
		i, err := v.Int64()
		if err != nil {
			return nil, fmt.Errorf("failed to convert json.Number to int64: %w", err)
		}
		return dang.IntValue{Val: int(i)}, nil
	case bool:
		return dang.BoolValue{Val: v}, nil
	case []any:
		listT, isList := fieldType.(dang.ListType)
		if !isList {
			return nil, fmt.Errorf("expected list type, got %T", fieldType)
		}
		vals := dang.ListValue{
			ElemType: listT,
		}
		for _, item := range v {
			val, err := anyToDang(ctx, env, item, listT.Type)
			if err != nil {
				return nil, fmt.Errorf("failed to convert list item: %w", err)
			}
			vals.Elements = append(vals.Elements, val)
		}
		return vals, nil
	case map[string]any:
		mod, isMod := fieldType.(dang.Env)
		if !isMod {
			return nil, fmt.Errorf("expected module type, got %T", fieldType)
		}
		modVal := dang.NewModuleValue(mod)
		modVal.SetDynamicScope(modVal)
		for name, val := range v {
			expectedT, found := mod.SchemeOf(name)
			if !found {
				return nil, fmt.Errorf("module %q does not have a scheme for %q", mod.Name(), name)
			}
			t, isMono := expectedT.Type()
			if !isMono {
				return nil, fmt.Errorf("expected monomorphic type, got %T", t)
			}
			dangVal, err := anyToDang(ctx, env, val, t)
			if err != nil {
				return nil, fmt.Errorf("failed to convert map item %q: %w", name, err)
			}
			modVal.Set(name, dangVal)
		}
		if mod.Name() != "" {
			constructor, ok := env.Get(mod.Name())
			if ok {
				if constructorFn, ok := constructor.(*dang.ConstructorFunction); ok {
					bodyEnv := dang.CreateCompositeEnv(modVal, env)
					_, err := dang.EvaluateFormsWithPhases(ctx, constructorFn.ClassBodyForms, bodyEnv)
					if err != nil {
						return nil, fmt.Errorf("evaluating class body for %s: %w", mod.Name(), err)
					}
				}
			}
		}
		return modVal, nil
	case nil:
		return dang.NullValue{}, nil
	default:
		return nil, fmt.Errorf("unsupported type %T", val)
	}
}
