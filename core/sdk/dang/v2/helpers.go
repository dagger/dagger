package dangv2

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/Khan/genqlient/graphql"
	"github.com/iancoleman/strcase"

	"github.com/dagger/dagger/core"
	dangshared "github.com/dagger/dagger/core/sdk/dang/shared"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	telemetry "github.com/dagger/otel-go"
	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/hm"
	"github.com/vito/dang/v2/pkg/introspection"
	"github.com/vito/dang/v2/pkg/ioctx"
	"github.com/vito/dang/v2/pkg/querybuilder"
)

type dangSourceRunner func(context.Context, string) (dang.ValueScope, error)

// dangModule identifies the module currently being evaluated. It lets the
// runtime map the module's own local type names (e.g. an interface "Overlay")
// to the namespaced names present in its runtime schema (e.g. "ModuleAOverlay")
// when marshaling received values back into GraphQL queries.
type dangModule struct {
	name         string // installed, namespaced module name
	originalName string // module name as written in source
}

func (r *runtime) eval(
	ctx context.Context,
	query *core.Query,
	schemaFile dagql.Result[*core.File],
	nestedClientMetadata *engine.ClientMetadata,
	callerClientID string,
	hostServiceProxyToCaller bool,
	fnCall *core.FunctionCall,
	moduleContext dagql.ObjectResult[*core.Module],
	workspaceContext dagql.ObjectResult[*core.Workspace],
) ([]byte, error) {
	return evalDangSource(ctx, query, r.modSource, schemaFile, nestedClientMetadata, callerClientID, hostServiceProxyToCaller, fnCall, moduleContext, workspaceContext, func(ctx context.Context, modSrcDir string) (dang.ValueScope, error) {
		return dang.RunDir(ctx, modSrcDir, false)
	}, func(ctx context.Context, env dang.ValueScope) ([]byte, error) {
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

		self := moduleContext.Self()
		module := dangModule{name: self.Name(), originalName: self.OriginalName}

		result, err := callDangFunction(ctx, env, fnCall, module)
		if err != nil {
			return nil, err
		}

		if flushErr := query.Server.FlushSessionTelemetry(ctx); flushErr != nil {
			slog.Debug("failed to flush telemetry after Dang eval", "error", flushErr)
		}

		return json.Marshal(result)
	})
}

func evalDangSource(
	ctx context.Context,
	query *core.Query,
	modSource dagql.ObjectResult[*core.ModuleSource],
	schemaFile dagql.Result[*core.File],
	nestedClientMetadata *engine.ClientMetadata,
	callerClientID string,
	hostServiceProxyToCaller bool,
	fnCall *core.FunctionCall,
	moduleContext dagql.ObjectResult[*core.Module],
	workspaceContext dagql.ObjectResult[*core.Workspace],
	runSource dangSourceRunner,
	withEnv func(context.Context, dang.ValueScope) ([]byte, error),
) ([]byte, error) {
	return dangshared.WithNestedClientServer(ctx, query, nestedClientMetadata, callerClientID, hostServiceProxyToCaller, fnCall, moduleContext, workspaceContext, func(ctx context.Context, gqlClient graphql.Client) ([]byte, error) {
		var intro introspection.Response
		f, err := schemaFile.Self().Open(ctx, dagql.ObjectResult[*core.File]{Result: schemaFile})
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

		modCtx := modSource.Self().ContextDirectory
		var env dang.ValueScope
		err = modCtx.Self().Mount(ctx, modCtx, func(path string) error {
			modSrcDir := filepath.Join(path, modSource.Self().SourceSubpath)

			// During the typedef/declaration phase (ModuleTypes) the schema
			// handed to us is deps-only: it does not yet carry the module's own
			// object/interface/enum types, because those are exactly what this
			// pass produces. Self-call fields annotate their return as
			// Dagger.<T> — the module's own type as it lives in the runtime
			// schema, carrying a GraphQL id + Node, not the bare local type — so
			// make every such name resolvable here by parsing the module source
			// for its declared types. At runtime the served schema already
			// includes the module's own types (Module.IncludeSelfInDeps), so
			// this is a no-op then.
			ensureModuleSelfTypes(intro.Schema, modSource.Self(), modSrcDir)

			env, err = runSource(ctx, modSrcDir)
			if err != nil {
				return fmt.Errorf("run dir: %w", err)
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("mount source: %w", err)
		}

		return withEnv(ctx, env)
	})
}

// ensureModuleSelfTypes makes each of the module's own declared object,
// interface, enum and scalar types resolvable as Dagger.<T> during the
// deps-only declaration phase (ModuleTypes), where the schema does not yet
// carry the module's own installed types — those are exactly what that pass
// produces. Self-call fields declare their return as Dagger.<T> because a
// self-call (e.g. `tuiQa`, or `test.fresh` returning the module's own type)
// yields the type as it lives in the runtime schema (carrying a GraphQL id,
// implementing Node), not the bare local type.
//
// The names are namespaced (via core.NamespaceObject) to match what the engine
// installs and what the runtime schema exposes once Module.IncludeSelfInDeps is
// in effect: the main object keeps the module's final name, secondary types are
// module-prefixed (a `type Widget` in module `Test` becomes `TestWidget`).
//
// The synthesized types are only ever used for name resolution and typedef
// references (via `withObject(name:)` / `withInterface(name:)`, which core then
// namespaces consistently); they are never emitted as TypeDefs, so a minimal
// shape suffices. Once the served runtime schema already carries the types
// (Module.IncludeSelfInDeps), this is a no-op.
func ensureModuleSelfTypes(schema *introspection.Schema, src *core.ModuleSource, modSrcDir string) {
	if schema == nil || src == nil {
		return
	}
	moduleName := src.ModuleName
	if moduleName == "" {
		moduleName = src.ModuleOriginalName
	}
	if moduleName == "" {
		return
	}

	for _, localName := range moduleDeclaredTypeNames(modSrcDir, moduleName) {
		schemaName := core.NamespaceObject(localName, moduleName, src.ModuleOriginalName)
		if schema.Types.Get(schemaName) != nil {
			continue
		}
		schema.Types = append(schema.Types, &introspection.Type{
			Kind: introspection.TypeKindObject,
			Name: schemaName,
			Fields: []*introspection.Field{
				{
					Name: "id",
					TypeRef: &introspection.TypeRef{
						Kind: introspection.TypeKindNonNull,
						OfType: &introspection.TypeRef{
							Kind: introspection.TypeKindScalar,
							Name: "ID",
						},
					},
				},
			},
		})
	}
}

// moduleDeclaredTypeNames parses the module's .dang source files and returns the
// local names of every public top-level type declaration (object, interface,
// enum, scalar). Only top-level declarations become module types in the schema,
// so types nested inside a body are intentionally ignored. The main object type
// — whose local name matches the module name — is always included even if the
// source can't be parsed, so the common case keeps working. Parsing here is
// best-effort: it drives name resolution only, and any genuine syntax error
// surfaces later when the source is actually declared/run.
func moduleDeclaredTypeNames(modSrcDir, moduleName string) []string {
	seen := map[string]struct{}{}
	var names []string
	add := func(name string) {
		if name == "" {
			return
		}
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}

	// The main object's local name matches the module name (capitalized camel
	// case); NamespaceObject collapses it to the module's final name. Seed it
	// unconditionally so a self-call returning the main type resolves even if
	// the rest of the source fails to parse.
	add(strcase.ToCamel(moduleName))

	entries, err := os.ReadDir(modSrcDir)
	if err != nil {
		slog.Debug("ensureModuleSelfTypes: read module dir", "dir", modSrcDir, "error", err)
		return names
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".dang" {
			continue
		}
		root, err := dang.ParseFile(filepath.Join(modSrcDir, entry.Name()))
		if err != nil {
			slog.Debug("ensureModuleSelfTypes: parse module file", "file", entry.Name(), "error", err)
			continue
		}
		file, ok := root.(*dang.FileBlock)
		if !ok {
			continue
		}
		// Only top-level type declarations become module types in the schema;
		// types declared inside a body are not hoisted, so iterate the file's
		// own forms rather than walking the AST recursively.
		for _, form := range file.Forms {
			switch decl := form.(type) {
			case *dang.ObjectDecl:
				if decl.Visibility >= dang.PublicVisibility && decl.Name != nil {
					add(decl.Name.Name)
				}
			case *dang.InterfaceDecl:
				if decl.Visibility >= dang.PublicVisibility && decl.Name != nil {
					add(decl.Name.Name)
				}
			case *dang.EnumDecl:
				if decl.Visibility >= dang.PublicVisibility && decl.Name != nil {
					add(decl.Name.Name)
				}
			case *dang.ScalarDecl:
				if decl.Visibility >= dang.PublicVisibility && decl.Name != nil {
					add(decl.Name.Name)
				}
			}
		}
	}
	return names
}

func runDangDirForModuleTypes(ctx context.Context, dirPath string) (dang.ValueScope, error) {
	env, err := dang.DeclareDir(ctx, dirPath, false)
	if err != nil {
		return nil, fmt.Errorf("declare Dang module types: %w", err)
	}
	return env, nil
}

func callDangFunction(ctx context.Context, env dang.ValueScope, fnCall *core.FunctionCall, module dangModule) (dang.Value, error) {
	inputArgs := make(map[string][]byte, len(fnCall.InputArgs))
	for _, arg := range fnCall.InputArgs {
		inputArgs[arg.Name] = []byte(arg.Value)
	}

	parentModBase, found, err := env.Lookup(ctx, fnCall.ParentName)
	if err != nil {
		return nil, fmt.Errorf("lookup parent type %s: %w", fnCall.ParentName, err)
	}
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
	parentModType := parentConstructor.ObjectType
	// Use the class file's captured env so rehydrated method calls keep the
	// imports that were in scope when the type was declared.
	parentClosure := parentConstructor.Closure

	// Argument and parent-state values are converted against the captured
	// closure and the executing module's identity (for local-type namespacing).
	conv := dangConverter{env: parentClosure, module: module}

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
		dangVal, err := conv.convert(ctx, val, argType)
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

	parentModEnv := dang.NewObject(parentModType)

	for name, value := range parentState {
		scheme, found := parentModType.SchemeOf(name)
		if !found {
			return nil, fmt.Errorf("unknown field: %s", name)
		}
		fieldType, isMono := scheme.Type()
		if !isMono {
			return nil, fmt.Errorf("non-monotype field %s", name)
		}
		dangVal, err := conv.convert(ctx, value, fieldType)
		if err != nil {
			return nil, fmt.Errorf("convert field %s: %w", name, err)
		}
		parentModEnv.Bind(name, dangVal, dang.PrivateVisibility)
	}

	bodyEnv := dang.CreateOverlayValueScope(parentModEnv, parentClosure)
	bodyEnv.EnterSelf(parentModEnv)
	_, err = dang.EvaluateFormsWithPhases(ctx, parentConstructor.ObjectBodyForms, bodyEnv)
	if err != nil {
		return nil, fmt.Errorf("evaluating class body for %s: %w", parentConstructor.ObjectName, err)
	}

	call := &dang.FunCall{
		Fun: &dang.Select{
			Receiver: &dang.ValueNode{Val: parentModEnv},
			Field:    &dang.Symbol{Name: fnCall.Name},
		},
		Args: args,
	}
	return call.Eval(ctx, bodyEnv)
}

type dangLocalTypes struct {
	modules map[*dang.Type]struct{}
}

func dangEvalModule(env dang.ValueScope) (dang.TypeScope, bool) {
	modVal, ok := env.(*dang.Object)
	if !ok {
		return nil, false
	}
	return modVal.Mod, true
}

func collectDangLocalTypes(env dang.ValueScope) dangLocalTypes {
	local := dangLocalTypes{modules: map[*dang.Type]struct{}{}}
	mod, ok := dangEvalModule(env)
	if !ok {
		return local
	}
	for name, namedType := range mod.NamedTypes() {
		origin, found := mod.LocalTypeOrigin(name)
		if !found || origin.Kind != dang.BindingOriginLocal {
			continue
		}
		if localMod, ok := namedType.(*dang.Type); ok {
			local.modules[localMod] = struct{}{}
		}
	}
	return local
}

func isDangLocalValueBinding(env dang.ValueScope, name string) bool {
	mod, ok := dangEvalModule(env)
	if !ok {
		return true
	}
	origin, found := mod.LocalValueOrigin(name)
	return !found || origin.Kind == dang.BindingOriginLocal
}

func (local dangLocalTypes) contains(mod *dang.Type) bool {
	_, ok := local.modules[mod]
	return ok
}

func dangLocalTypeName(name string) string {
	return "DangSDKLocalType" + name
}

func withDangLocalName[T dagql.Typed](
	ctx context.Context,
	srv *dagql.Server,
	typeDef dagql.ObjectResult[*core.TypeDef],
	res dagql.ObjectResult[T],
	temporaryName string,
	kind string,
	withField string,
	argName string,
) (dagql.ObjectResult[*core.TypeDef], error) {
	var renamed dagql.ObjectResult[T]
	if err := srv.Select(ctx, res, &renamed, dagql.Selector{
		Field: "__withName",
		Args:  []dagql.NamedInput{{Name: "name", Value: dagql.String(temporaryName)}},
	}); err != nil {
		return typeDef, fmt.Errorf("mark local %s type: %w", kind, err)
	}
	renamedID, err := core.ResultIDInput(renamed)
	if err != nil {
		return typeDef, fmt.Errorf("local %s type ID: %w", kind, err)
	}
	var updated dagql.ObjectResult[*core.TypeDef]
	if err := srv.Select(ctx, typeDef, &updated, dagql.Selector{
		Field: withField,
		Args:  []dagql.NamedInput{{Name: argName, Value: renamedID}},
	}); err != nil {
		return typeDef, fmt.Errorf("mark local %s typedef: %w", kind, err)
	}
	return updated, nil
}

// markDangLocalType gives a local Dang type a temporary, non-core schema name
// while preserving its OriginalName. Core's Module.WithObject namespacing later
// uses OriginalName to produce the final module-prefixed name; the temporary
// Name only prevents local references like Container from being mistaken for
// daggercore.Container before namespacing runs.
func markDangLocalType(ctx context.Context, srv *dagql.Server, typeDef dagql.ObjectResult[*core.TypeDef], name string) (dagql.ObjectResult[*core.TypeDef], error) {
	if name == "" || typeDef.Self() == nil {
		return typeDef, nil
	}
	temporaryName := dangLocalTypeName(name)
	switch typeDef.Self().Kind {
	case core.TypeDefKindObject:
		return withDangLocalName(ctx, srv, typeDef, typeDef.Self().AsObject.Value, temporaryName, "object", "__withObjectTypeDef", "objectTypeDef")
	case core.TypeDefKindInterface:
		return withDangLocalName(ctx, srv, typeDef, typeDef.Self().AsInterface.Value, temporaryName, "interface", "__withInterfaceTypeDef", "interfaceTypeDef")
	case core.TypeDefKindEnum:
		return withDangLocalName(ctx, srv, typeDef, typeDef.Self().AsEnum.Value, temporaryName, "enum", "__withEnumTypeDef", "enumTypeDef")
	default:
		return typeDef, nil
	}
}

func initDangModule(ctx context.Context, srv *dagql.Server, env dang.ValueScope) (res dagql.ObjectResult[*core.Module], _ error) {
	localTypes := collectDangLocalTypes(env)
	sels := []dagql.Selector{
		{
			Field: "module",
		},
	}

	binds := env.Bindings(dang.PublicVisibility)
	for _, binding := range binds {
		if !isDangLocalValueBinding(env, binding.Key) {
			continue
		}
		switch val := binding.Value.(type) {
		case *dang.ConstructorFunction:
			objDef, err := createObjectTypeDef(ctx, srv, binding.Key, val, localTypes)
			if err != nil {
				return res, fmt.Errorf("failed to create object %s: %w", binding.Key, err)
			}
			fnDef, err := createFunction(ctx, srv, val.ObjectType, binding.Key, val.FnType, val.Closure, localTypes)
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

		case *dang.Object:
			mod, ok := val.Mod.(*dang.Type)
			if !ok {
				slog.Warn("skipping non-module module value", "name", binding.Key)
				break
			}
			switch mod.Kind {
			case dang.EnumKind:
				enumDef, err := createEnumTypeDef(ctx, srv, binding.Key, val, localTypes)
				if err != nil {
					return res, fmt.Errorf("failed to create enum %s: %w", binding.Key, err)
				}
				enumDefID, err := enumDef.ID()
				if err != nil {
					return res, fmt.Errorf("failed to get enum typedef ID for %s: %w", binding.Key, err)
				}
				sels = append(sels, dagql.Selector{
					Field: "withEnum",
					Args:  []dagql.NamedInput{{Name: "enum", Value: dagql.NewID[*core.TypeDef](enumDefID)}},
				})
			case dang.ScalarKind:
				slog.Info("skipping scalar module value (handled as string type)", "name", binding.Key)
			case dang.InterfaceKind:
				interfaceDef, err := createInterfaceTypeDef(ctx, srv, binding.Key, val, env, localTypes)
				if err != nil {
					return res, fmt.Errorf("failed to create interface %s: %w", binding.Key, err)
				}
				interfaceDefID, err := interfaceDef.ID()
				if err != nil {
					return res, fmt.Errorf("failed to get interface typedef ID for %s: %w", binding.Key, err)
				}
				sels = append(sels, dagql.Selector{
					Field: "withInterface",
					Args:  []dagql.NamedInput{{Name: "iface", Value: dagql.NewID[*core.TypeDef](interfaceDefID)}},
				})
			default:
				slog.Warn("unknown module kind", "name", binding.Key, "kind", mod.Kind)
			}

		default:
			slog.Info("skipping non-class public binding", "name", binding.Key, "type", fmt.Sprintf("%T", val))
		}
	}

	if err := srv.Select(ctx, srv.Root(), &res, sels...); err != nil {
		return res, fmt.Errorf("failed to select module: %w", err)
	}

	return res, nil
}

// createFunction builds a core.Function for the named slot. directiveScope is the
// file-local scope used to evaluate directive arguments; it must be the owning
// type's captured closure (ConstructorFunction.Closure) so that directive args
// referencing imported symbols (e.g. @cache(policy: FunctionCachePolicy.Never))
// resolve the same way the type's own source does. See issue #13440.
func createFunction(ctx context.Context, srv *dagql.Server, mod *dang.Type, name string, fn *hm.FunctionType, directiveScope dang.ValueScope, localTypes dangLocalTypes) (dagql.ObjectResult[*core.Function], error) {
	var res dagql.ObjectResult[*core.Function]

	retTypeDef, err := dangTypeToTypeDef(ctx, srv, fn.Ret(false), localTypes)
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

	if desc, ok := mod.GetDocString(name); ok {
		sels = append(sels, dagql.Selector{
			Field: "withDescription",
			Args:  []dagql.NamedInput{{Name: "description", Value: dagql.String(desc)}},
		})
	}

	dirSels, err := functionDirectiveSelectors(ctx, directiveScope, mod.GetDirectives(name))
	if err != nil {
		return res, fmt.Errorf("directives for %s: %w", name, err)
	}
	sels = append(sels, dirSels...)

	args := fn.Arg().(*dang.RecordType)
	for _, arg := range args.Fields {
		argType, mono := arg.Value.Type()
		if !mono {
			return res, fmt.Errorf("non-monotype argument %s", arg.Key)
		}
		typeDef, err := dangTypeToTypeDef(ctx, srv, argType, localTypes)
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

		if doc := args.DocStrings[arg.Key]; doc != "" {
			argArgs = append(argArgs, dagql.NamedInput{Name: "description", Value: dagql.String(doc)})
		}

		argArgs, err = applyArgDirectives(ctx, directiveScope, argArgs, arg.Key, args.Directives)
		if err != nil {
			return res, err
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

// functionDirectiveSelectors converts function-level directives (@check,
// @generate, @up, @agent, @cache) into dagql selectors.
func functionDirectiveSelectors(ctx context.Context, env dang.ValueScope, directives []*dang.DirectiveApplication) ([]dagql.Selector, error) {
	var sels []dagql.Selector
	for _, directive := range directives {
		switch directive.Name {
		case "check":
			sels = append(sels, dagql.Selector{Field: "withCheck"})
		case "generate":
			sels = append(sels, dagql.Selector{Field: "withGenerator"})
		case "up":
			sels = append(sels, dagql.Selector{Field: "withUp"})
		case "agent":
			sels = append(sels, dagql.Selector{Field: "withAgent"})
		case "cache":
			sel, err := cacheDirectiveSelector(ctx, env, directive)
			if err != nil {
				return nil, err
			}
			sels = append(sels, sel)
		}
	}
	return sels, nil
}

// cacheDirectiveSelector converts a @cache directive into a withCachePolicy selector.
func cacheDirectiveSelector(ctx context.Context, env dang.ValueScope, directive *dang.DirectiveApplication) (dagql.Selector, error) {
	var policy core.FunctionCachePolicy
	var ttl string
	for _, arg := range directive.Args {
		val, err := evalDirectiveArg(ctx, env, arg.Value)
		if err != nil {
			return dagql.Selector{}, fmt.Errorf("failed to evaluate @cache argument %s: %w", arg.Key, err)
		}
		switch arg.Key {
		case "policy":
			if s, ok := val.(string); ok {
				policy = core.FunctionCachePolicy(s)
			}
		case "ttl":
			if s, ok := val.(string); ok {
				ttl = s
			}
		}
	}
	if policy == "" {
		policy = core.FunctionCachePolicyDefault
	}
	args := []dagql.NamedInput{
		{Name: "policy", Value: policy},
	}
	if ttl != "" {
		args = append(args, dagql.NamedInput{Name: "timeToLive", Value: dagql.Opt(dagql.String(ttl))})
	}
	return dagql.Selector{Field: "withCachePolicy", Args: args}, nil
}

// applyArgDirectives processes argument-level directives (@defaultPath,
// @ignorePatterns) and appends the resulting inputs to argArgs.
func applyArgDirectives(ctx context.Context, env dang.ValueScope, argArgs []dagql.NamedInput, argName string, allDirs []dang.Keyed[[]*dang.DirectiveApplication]) ([]dagql.NamedInput, error) {
	for _, argDirs := range allDirs {
		if argDirs.Key != argName {
			continue
		}
		for _, dir := range argDirs.Value {
			switch dir.Name {
			case "defaultPath":
				for _, a := range dir.Args {
					if a.Key == "path" { // TODO: positional
						val, err := evalDirectiveArg(ctx, env, a.Value)
						if err != nil {
							return nil, fmt.Errorf("@defaultPath.path for %s: %w", argName, err)
						}
						if path, ok := val.(string); ok {
							argArgs = append(argArgs, dagql.NamedInput{Name: "defaultPath", Value: dagql.String(path)})
						}
					}
				}
			case "ignorePatterns":
				for _, a := range dir.Args {
					if a.Key == "patterns" {
						val, err := evalDirectiveArg(ctx, env, a.Value)
						if err != nil {
							return nil, fmt.Errorf("@ignorePatterns.patterns for %s: %w", argName, err)
						}
						ignore, ok := val.([]any)
						if !ok {
							return nil, fmt.Errorf("@ignorePatterns for %s: expected []any, got %T", argName, val)
						}
						var patterns []string
						for _, p := range ignore {
							str, ok := p.(string)
							if !ok {
								return nil, fmt.Errorf("@ignorePatterns for %s: expected string element, got %T", argName, p)
							}
							patterns = append(patterns, str)
						}
						if len(patterns) > 0 {
							argArgs = append(argArgs, dagql.NamedInput{Name: "ignore", Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(patterns...))})
						}
					}
				}
			}
		}
	}
	return argArgs, nil
}

// evalDirectiveArg evaluates a directive argument AST node through the Dang
// runtime and converts the resulting Value to a plain Go value.
func evalDirectiveArg(ctx context.Context, env dang.ValueScope, node dang.Node) (any, error) {
	val, err := dang.EvalNode(ctx, env, node)
	if err != nil {
		return nil, err
	}
	return dangValToGo(val)
}

// dangValToGo converts a Dang Value to a plain Go value.
func dangValToGo(val dang.Value) (any, error) {
	switch v := val.(type) {
	case dang.StringValue:
		return v.Val, nil
	case dang.IntValue:
		return v.Val, nil
	case dang.BoolValue:
		return v.Val, nil
	case dang.EnumValue:
		return v.Val, nil
	case dang.ListValue:
		elements := make([]any, len(v.Elements))
		for i, elem := range v.Elements {
			g, err := dangValToGo(elem)
			if err != nil {
				return nil, fmt.Errorf("list element %d: %w", i, err)
			}
			elements[i] = g
		}
		return elements, nil
	case dang.NullValue:
		return nil, nil
	default:
		return nil, fmt.Errorf("unsupported directive argument value type: %T", val)
	}
}

func createObjectTypeDef(ctx context.Context, srv *dagql.Server, name string, module *dang.ConstructorFunction, localTypes dangLocalTypes) (dagql.ObjectResult[*core.TypeDef], error) {
	var res dagql.ObjectResult[*core.TypeDef]

	classMod := module.ObjectType
	withObjectArgs := []dagql.NamedInput{{Name: "name", Value: dagql.String(name)}}
	if desc := classMod.GetTypeDocString(); desc != "" {
		withObjectArgs = append(withObjectArgs, dagql.NamedInput{Name: "description", Value: dagql.String(desc)})
	}

	sels := []dagql.Selector{
		{Field: "typeDef"},
		{
			Field: "withObject",
			Args:  withObjectArgs,
		},
	}

	for _, form := range module.ObjectBodyForms {
		slot, ok := form.(*dang.FieldDecl)
		if !ok || slot.Visibility < dang.PublicVisibility {
			continue
		}

		bindingName := slot.Name.Name
		scheme, found := classMod.LocalSchemeOf(bindingName)
		if !found {
			return res, fmt.Errorf("missing local slot %s for %s", bindingName, name)
		}

		slotType, isMono := scheme.Type()
		if !isMono {
			return res, fmt.Errorf("non-monotype method %s", bindingName)
		}
		switch x := slotType.(type) {
		case *hm.FunctionType:
			fnDef, err := createFunction(ctx, srv, classMod, bindingName, x, module.Closure, localTypes)
			if err != nil {
				return res, fmt.Errorf("failed to create method %s for %s: %w", bindingName, name, err)
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
			fieldDef, err := dangTypeToTypeDef(ctx, srv, slotType, localTypes)
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

			if desc, ok := classMod.GetDocString(bindingName); ok {
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
	if localTypes.contains(classMod) {
		var err error
		res, err = markDangLocalType(ctx, srv, res, name)
		if err != nil {
			return res, fmt.Errorf("failed to mark local object typedef: %w", err)
		}
	}

	return res, nil
}

func dangTypeToTypeDef(ctx context.Context, srv *dagql.Server, dangType hm.Type, localTypes dangLocalTypes) (dagql.ObjectResult[*core.TypeDef], error) {
	var res dagql.ObjectResult[*core.TypeDef]

	sels := []dagql.Selector{{Field: "typeDef"}}

	if nonNull, isNonNull := dangType.(hm.NonNullType); isNonNull {
		inner, err := dangTypeToTypeDef(ctx, srv, nonNull.Type, localTypes)
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
	case dang.MapType:
		return res, fmt.Errorf("%s cannot be exposed via GraphQL; store maps in private (let) fields instead", t)
	case dang.ListType:
		elemTypeDef, err := dangTypeToTypeDef(ctx, srv, t.Type, localTypes)
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
	case *dang.Type:
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
			switch t.Kind {
			case dang.EnumKind:
				sels = append(sels, dagql.Selector{
					Field: "withEnum",
					Args:  []dagql.NamedInput{{Name: "name", Value: dagql.String(t.Named)}},
				})
			case dang.ScalarKind:
				sels = append(sels, dagql.Selector{
					Field: "withKind",
					Args:  []dagql.NamedInput{{Name: "kind", Value: core.TypeDefKindString}},
				})
			case dang.InterfaceKind:
				sels = append(sels, dagql.Selector{
					Field: "withInterface",
					Args:  []dagql.NamedInput{{Name: "name", Value: dagql.String(t.Named)}},
				})
			default:
				sels = append(sels, dagql.Selector{
					Field: "withObject",
					Args:  []dagql.NamedInput{{Name: "name", Value: dagql.String(t.Named)}},
				})
			}
		}
	default:
		return res, fmt.Errorf("unknown type: %T: %s", dangType, dangType)
	}

	if err := srv.Select(ctx, srv.Root(), &res, sels...); err != nil {
		return res, fmt.Errorf("failed to select typedef: %w", err)
	}
	if mod, ok := dangType.(*dang.Type); ok && localTypes.contains(mod) {
		var err error
		res, err = markDangLocalType(ctx, srv, res, mod.Named)
		if err != nil {
			return res, fmt.Errorf("failed to mark local typedef: %w", err)
		}
	}

	return res, nil
}

func createEnumTypeDef(ctx context.Context, srv *dagql.Server, name string, enumMod *dang.Object, localTypes dangLocalTypes) (dagql.ObjectResult[*core.TypeDef], error) {
	var res dagql.ObjectResult[*core.TypeDef]

	sels := []dagql.Selector{
		{Field: "typeDef"},
		{
			Field: "withEnum",
			Args:  []dagql.NamedInput{{Name: "name", Value: dagql.String(name)}},
		},
	}

	for memberName, val := range enumMod.Values {
		if _, ok := val.(dang.EnumValue); !ok {
			continue
		}
		sels = append(sels, dagql.Selector{
			Field: "withEnumMember",
			Args:  []dagql.NamedInput{{Name: "name", Value: dagql.String(memberName)}},
		})
	}

	if err := srv.Select(ctx, srv.Root(), &res, sels...); err != nil {
		return res, fmt.Errorf("failed to create enum typedef: %w", err)
	}
	if mod, ok := enumMod.Mod.(*dang.Type); ok && localTypes.contains(mod) {
		var err error
		res, err = markDangLocalType(ctx, srv, res, name)
		if err != nil {
			return res, fmt.Errorf("failed to mark local enum typedef: %w", err)
		}
	}

	return res, nil
}

func createInterfaceTypeDef(ctx context.Context, srv *dagql.Server, name string, interfaceMod *dang.Object, env dang.ValueScope, localTypes dangLocalTypes) (dagql.ObjectResult[*core.TypeDef], error) {
	var res dagql.ObjectResult[*core.TypeDef]

	mod, ok := interfaceMod.Mod.(*dang.Type)
	if !ok {
		return res, fmt.Errorf("expected *dang.Type for interface %s, got %T", name, interfaceMod.Mod)
	}

	sels := []dagql.Selector{
		{Field: "typeDef"},
		{
			Field: "withInterface",
			Args:  []dagql.NamedInput{{Name: "name", Value: dagql.String(name)}},
		},
	}

	for fieldName, scheme := range mod.Bindings(dang.PublicVisibility) {
		fieldType, isMono := scheme.Type()
		if !isMono {
			return res, fmt.Errorf("non-monotype field %s in interface %s", fieldName, name)
		}
		switch x := fieldType.(type) {
		case *hm.FunctionType:
			fnDef, err := createFunction(ctx, srv, mod, fieldName, x, env, localTypes)
			if err != nil {
				return res, fmt.Errorf("failed to create method %s for interface %s: %w", fieldName, name, err)
			}
			fnDefID, err := fnDef.ID()
			if err != nil {
				return res, fmt.Errorf("failed to get function ID for %s: %w", fieldName, err)
			}
			sels = append(sels, dagql.Selector{
				Field: "withFunction",
				Args:  []dagql.NamedInput{{Name: "function", Value: dagql.NewID[*core.Function](fnDefID)}},
			})
		default:
			fieldTypeDef, err := dangTypeToTypeDef(ctx, srv, fieldType, localTypes)
			if err != nil {
				return res, fmt.Errorf("failed to create field %s for interface %s: %w", fieldName, name, err)
			}
			fieldTypeDefID, err := fieldTypeDef.ID()
			if err != nil {
				return res, fmt.Errorf("failed to get field type ID for %s: %w", fieldName, err)
			}
			fieldArgs := []dagql.NamedInput{
				{Name: "name", Value: dagql.String(fieldName)},
				{Name: "typeDef", Value: dagql.NewID[*core.TypeDef](fieldTypeDefID)},
			}
			if desc, ok := mod.GetDocString(fieldName); ok {
				fieldArgs = append(fieldArgs, dagql.NamedInput{Name: "description", Value: dagql.String(desc)})
			}
			sels = append(sels, dagql.Selector{
				Field: "withField",
				Args:  fieldArgs,
			})
		}
	}

	if err := srv.Select(ctx, srv.Root(), &res, sels...); err != nil {
		return res, fmt.Errorf("failed to create interface typedef: %w", err)
	}
	if localTypes.contains(mod) {
		var err error
		res, err = markDangLocalType(ctx, srv, res, name)
		if err != nil {
			return res, fmt.Errorf("failed to mark local interface typedef: %w", err)
		}
	}

	return res, nil
}

// dangConverter converts the JSON values decoded from a function call's
// arguments and parent state into Dang values. It resolves names against a
// fixed scope (the parent type's captured closure) and carries the executing
// module's identity, so a module's own local type names can be mapped to the
// namespaced names its runtime schema actually exposes.
type dangConverter struct {
	env    dang.ValueScope
	module dangModule
}

// convert turns a decoded JSON value into the Dang value expected by fieldType.
func (c dangConverter) convert(ctx context.Context, val any, fieldType hm.Type) (dang.Value, error) {
	if nonNull, ok := fieldType.(hm.NonNullType); ok {
		return c.convert(ctx, val, nonNull.Type)
	}
	switch v := val.(type) {
	case string:
		return c.convertString(ctx, v, fieldType)
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
		return c.convertList(ctx, v, fieldType)
	case map[string]any:
		return c.convertObject(ctx, v, fieldType)
	case nil:
		return dang.NullValue{}, nil
	default:
		return nil, fmt.Errorf("unsupported type %T", val)
	}
}

func (c dangConverter) convertString(ctx context.Context, val string, fieldType hm.Type) (dang.Value, error) {
	modType, isMod := fieldType.(*dang.Type)
	if !isMod || modType == dang.StringType {
		return dang.StringValue{Val: val}, nil
	}

	switch modType.Kind {
	case dang.EnumKind:
		return c.convertEnum(ctx, val, modType)
	case dang.ScalarKind:
		return dang.ScalarValue{Val: val, ScalarType: modType}, nil
	default:
		return c.convertObjectID(ctx, val, modType)
	}
}

func (c dangConverter) convertEnum(ctx context.Context, val string, modType *dang.Type) (dang.Value, error) {
	enumVal, found, err := c.env.Lookup(ctx, modType.Named)
	if err != nil {
		return nil, fmt.Errorf("lookup enum type %s: %w", modType.Named, err)
	}
	if !found {
		return nil, fmt.Errorf("enum type %s not found in environment", modType.Named)
	}

	enumMod, ok := enumVal.(*dang.Object)
	if !ok {
		return nil, fmt.Errorf("enum type %s not found in environment", modType.Named)
	}

	member, found, err := enumMod.Lookup(ctx, val)
	if err != nil {
		return nil, fmt.Errorf("lookup enum value %s.%s: %w", modType.Named, val, err)
	}
	if !found {
		return nil, fmt.Errorf("unknown enum value %s.%s", modType.Named, val)
	}
	return member, nil
}

func (c dangConverter) convertObjectID(ctx context.Context, id string, modType *dang.Type) (dang.Value, error) {
	nodeVal, found, err := c.env.Lookup(ctx, "node")
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("node field not found in environment")
	}
	nodeFn, ok := nodeVal.(dang.GraphQLFunction)
	if !ok {
		return nil, fmt.Errorf("node field is %T, not dang.GraphQLFunction", nodeVal)
	}

	// A module's own types are local in its source (e.g. "Overlay") but
	// module-namespaced in the schema it queries against ("ModuleAOverlay").
	// Resolve the local name to its namespaced schema name so the node query and
	// inline fragment reference a type that actually exists in the schema.
	schemaName := c.schemaTypeName(nodeFn.Schema, modType.Named)

	field := *nodeFn.Field
	field.TypeRef = &introspection.TypeRef{
		Kind: introspection.TypeKindNonNull,
		OfType: &introspection.TypeRef{
			Kind: introspection.TypeKindObject,
			Name: schemaName,
		},
	}
	if schemaType := nodeFn.Schema.Types.Get(schemaName); schemaType != nil {
		field.TypeRef.OfType.Kind = schemaType.Kind
	}

	return dang.GraphQLValue{
		Name:       "node",
		TypeName:   schemaName,
		Field:      &field,
		ValType:    hm.NonNullType{Type: modType},
		Client:     nodeFn.Client,
		Schema:     nodeFn.Schema,
		TypeScope:  nodeFn.TypeScope,
		QueryChain: querybuilder.Query().Select("node").Arg("id", id).InlineFragment(schemaName),
	}, nil
}

// schemaTypeName maps a (possibly module-local) type name to the name it has in
// the given schema. A name already present is returned as-is (e.g. a dependency
// type, already namespaced). Otherwise it's one of the executing module's own
// local types and is namespaced to match what the engine installed (requires
// the SELF_CALLS feature so the module's own types are present in its runtime
// schema).
func (c dangConverter) schemaTypeName(schema *introspection.Schema, name string) string {
	if schema != nil && schema.Types.Get(name) != nil {
		return name
	}
	namespaced := core.NamespaceObject(name, c.module.name, c.module.originalName)
	if schema == nil || schema.Types.Get(namespaced) != nil {
		return namespaced
	}
	return name
}

func (c dangConverter) convertList(ctx context.Context, vals []any, fieldType hm.Type) (dang.Value, error) {
	listT, isList := fieldType.(dang.ListType)
	if !isList {
		return nil, fmt.Errorf("expected list type, got %T", fieldType)
	}

	listVal := dang.ListValue{ElemType: listT}
	for _, item := range vals {
		itemVal, err := c.convert(ctx, item, listT.Type)
		if err != nil {
			return nil, fmt.Errorf("failed to convert list item: %w", err)
		}
		listVal.Elements = append(listVal.Elements, itemVal)
	}
	return listVal, nil
}

// convertMap rehydrates a Map-typed field from its serialized JSON object.
// JSON decoding does not preserve key order, so keys are sorted to keep
// rehydrated maps deterministic.
func (c dangConverter) convertMap(ctx context.Context, vals map[string]any, mapT dang.MapType) (dang.Value, error) {
	mapVal := dang.MapValue{
		Keys:    make([]string, 0, len(vals)),
		Entries: make(map[string]dang.Value, len(vals)),
		ValType: mapT.Type,
	}
	for _, key := range slices.Sorted(maps.Keys(vals)) {
		entryVal, err := c.convert(ctx, vals[key], mapT.Type)
		if err != nil {
			return nil, fmt.Errorf("failed to convert map entry %q: %w", key, err)
		}
		mapVal.Keys = append(mapVal.Keys, key)
		mapVal.Entries[key] = entryVal
	}
	return mapVal, nil
}

func (c dangConverter) convertObject(ctx context.Context, vals map[string]any, fieldType hm.Type) (dang.Value, error) {
	if mapT, isMap := fieldType.(dang.MapType); isMap {
		return c.convertMap(ctx, vals, mapT)
	}

	mod, isMod := fieldType.(dang.TypeScope)
	if !isMod {
		return nil, fmt.Errorf("expected module type, got %T", fieldType)
	}

	modVal := dang.NewObject(mod)
	for name, val := range vals {
		expectedT, found := mod.SchemeOf(name)
		if !found {
			return nil, fmt.Errorf("module %q does not have a scheme for %q", mod.Name(), name)
		}
		t, isMono := expectedT.Type()
		if !isMono {
			return nil, fmt.Errorf("expected monomorphic type, got %T", t)
		}
		dangVal, err := c.convert(ctx, val, t)
		if err != nil {
			return nil, fmt.Errorf("failed to convert map item %q: %w", name, err)
		}
		modVal.Bind(name, dangVal, dang.PrivateVisibility)
	}

	if err := evaluateDangClassBody(ctx, c.env, mod, modVal); err != nil {
		return nil, err
	}
	return modVal, nil
}

func evaluateDangClassBody(ctx context.Context, env dang.ValueScope, mod dang.TypeScope, modVal *dang.Object) error {
	if mod.Name() == "" {
		return nil
	}

	constructor, found, err := env.Lookup(ctx, mod.Name())
	if err != nil {
		return fmt.Errorf("lookup constructor %s: %w", mod.Name(), err)
	}
	if !found {
		return nil
	}

	constructorFn, ok := constructor.(*dang.ConstructorFunction)
	if !ok {
		return nil
	}

	bodyEnv := dang.CreateOverlayValueScope(modVal, env)
	bodyEnv.EnterSelf(modVal)
	_, err = dang.EvaluateFormsWithPhases(ctx, constructorFn.ObjectBodyForms, bodyEnv)
	if err != nil {
		return fmt.Errorf("evaluating class body for %s: %w", mod.Name(), err)
	}
	return nil
}
