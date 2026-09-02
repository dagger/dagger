package dangv2

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/core"
	dangshared "github.com/dagger/dagger/core/sdk/dang/shared"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/engineutil"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/vito/dang/v2/pkg/dang"
)

const moduleEntrypointTypeName = "ModuleEntrypoint"

// EntrypointModuleTypes calls types on a built-in Dang module entrypoint.
func EntrypointModuleTypes(
	ctx context.Context,
	deps *core.SchemaBuilder,
	src dagql.ObjectResult[*core.ModuleSource],
	moduleContext dagql.ObjectResult[*core.Module],
) (inst dagql.ObjectResult[*core.Module], rerr error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("get Dagger server for entrypoint types: %w", err)
	}
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return inst, fmt.Errorf("get current query for entrypoint types: %w", err)
	}
	entrySrc, workspace, err := resolveEntrypointSource(ctx, dag, src)
	if err != nil {
		return inst, err
	}
	schemaJSONFile, err := deps.SchemaIntrospectionJSONFileForModule(ctx)
	if err != nil {
		return inst, fmt.Errorf("get entrypoint schema: %w", err)
	}
	clientMetadata, nestedClientMetadata, err := dangshared.NewNestedClientMetadata(ctx)
	if err != nil {
		return inst, err
	}

	var typeDefs dagql.ObjectResultArray[*core.TypeDef]
	_, err = evalDangSource(
		ctx,
		query,
		entrySrc,
		schemaJSONFile,
		nestedClientMetadata,
		clientMetadata.ClientID,
		true,
		nil,
		moduleContext,
		runEntrypointDir,
		func(ctx context.Context, env dang.ValueScope) ([]byte, error) {
			entrypointName, err := findModuleEntrypoint(env)
			if err != nil {
				return nil, err
			}
			workspaceArg, err := workspaceCallArg(workspace)
			if err != nil {
				return nil, err
			}
			result, err := callEntrypointMethod(ctx, env, entrypointName, "types", moduleContext, []*core.FunctionCallArgValue{
				workspaceArg,
			})
			if err != nil {
				return nil, fmt.Errorf("call module entrypoint types: %w", err)
			}
			typeDefs, err = loadEntrypointTypeDefs(ctx, dag, result)
			return nil, err
		},
	)
	if err != nil {
		return inst, dangshared.ConvertError(err)
	}
	if err := validateEntrypointConstructors(typeDefs); err != nil {
		return inst, err
	}
	return moduleFromEntrypointTypeDefs(ctx, dag, typeDefs)
}

type entrypointRuntime struct {
	deps      *core.SchemaBuilder
	modSource dagql.ObjectResult[*core.ModuleSource]
}

// NewEntrypointRuntime returns the built-in Dang module entrypoint runtime.
func NewEntrypointRuntime(
	deps *core.SchemaBuilder,
	source dagql.ObjectResult[*core.ModuleSource],
) core.ModuleRuntime {
	return &entrypointRuntime{deps: deps, modSource: source}
}

func (r *entrypointRuntime) AsContainer() (dagql.ObjectResult[*core.Container], bool) {
	return dagql.ObjectResult[*core.Container]{}, false
}

func (r *entrypointRuntime) Call(
	ctx context.Context,
	_ *engineutil.ExecutionMetadata,
	fnCall *core.FunctionCall,
	moduleContext dagql.ObjectResult[*core.Module],
) (rerr error) {
	defer func() {
		if rerr != nil {
			rerr = dangshared.ConvertError(rerr)
		}
	}()

	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return fmt.Errorf("get Dagger server for entrypoint call: %w", err)
	}
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return fmt.Errorf("get current query for entrypoint call: %w", err)
	}
	entrySrc, workspace, err := resolveEntrypointSource(ctx, dag, r.modSource)
	if err != nil {
		return err
	}
	schemaJSONFile, err := r.deps.SchemaIntrospectionJSONFileForModule(ctx)
	if err != nil {
		return fmt.Errorf("get entrypoint schema: %w", err)
	}
	clientMetadata, nestedClientMetadata, err := dangshared.NewNestedClientMetadata(ctx)
	if err != nil {
		return err
	}

	var resultJSON []byte
	_, err = evalDangSource(
		ctx,
		query,
		entrySrc,
		schemaJSONFile,
		nestedClientMetadata,
		clientMetadata.ClientID,
		true,
		fnCall,
		moduleContext,
		runEntrypointDir,
		func(ctx context.Context, env dang.ValueScope) ([]byte, error) {
			entrypointName, err := findModuleEntrypoint(env)
			if err != nil {
				return nil, err
			}
			workspaceArg, err := workspaceCallArg(workspace)
			if err != nil {
				return nil, err
			}
			fnArgs, err := functionArgsJSON(fnCall.InputArgs)
			if err != nil {
				return nil, err
			}
			result, err := callEntrypointMethod(ctx, env, entrypointName, "call", moduleContext, []*core.FunctionCallArgValue{
				workspaceArg,
				stringCallArg("receiverType", fnCall.ParentName),
				jsonScalarCallArg("receiverValue", fnCall.Parent),
				stringCallArg("fnName", fnCall.Name),
				jsonScalarCallArg("fnArgs", fnArgs),
			})
			if err != nil {
				return nil, fmt.Errorf("call module entrypoint: %w", err)
			}
			resultJSON, err = entrypointJSONResult(result)
			return nil, err
		},
	)
	if err != nil {
		return err
	}
	return fnCall.ReturnValue(ctx, core.JSON(resultJSON))
}

func resolveEntrypointSource(
	ctx context.Context,
	dag *dagql.Server,
	src dagql.ObjectResult[*core.ModuleSource],
) (dagql.ObjectResult[*core.ModuleSource], dagql.ObjectResult[*core.Workspace], error) {
	if src.Self().Entrypoint == nil {
		return dagql.ObjectResult[*core.ModuleSource]{}, dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("module entrypoint is not configured")
	}

	var workspace dagql.ObjectResult[*core.Workspace]
	if err := dag.Select(ctx, dag.Root(), &workspace, dagql.Selector{Field: "currentWorkspace"}); err != nil {
		return dagql.ObjectResult[*core.ModuleSource]{}, dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("get module workspace: %w", err)
	}

	address := src.Self().Entrypoint.Source
	var directory dagql.ObjectResult[*core.Directory]
	var err error
	if isWorkspaceRelativeEntrypointSource(address) {
		err = dag.Select(ctx, workspace, &directory, dagql.Selector{
			Field: "directory",
			Args:  []dagql.NamedInput{{Name: "path", Value: dagql.String(address)}},
		})
	} else {
		err = dag.Select(ctx, dag.Root(), &directory,
			dagql.Selector{Field: "address", Args: []dagql.NamedInput{{Name: "value", Value: dagql.String(address)}}},
			dagql.Selector{Field: "directory"},
		)
	}
	if err != nil {
		return dagql.ObjectResult[*core.ModuleSource]{}, dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("resolve module entrypoint source %q: %w", address, err)
	}

	entrySrc := src.Self().Clone()
	entrySrc.ContextDirectory = directory
	entrySrc.SourceRootSubpath = "."
	entrySrc.SourceSubpath = "."
	entrySrc.IncludePaths = nil
	entrySrc.RebasedIncludePaths = nil
	entrySrc.ConfigDependencies = nil
	entrySrc.Dependencies = nil
	entrySrc.ConfigBlueprint = nil
	entrySrc.Blueprint = dagql.ObjectResult[*core.ModuleSource]{}
	entrySrc.ConfigToolchains = nil
	entrySrc.Toolchains = nil
	entrySrc.Local = nil
	entrySrc.Git = nil
	entrySrc.DirSrc = &core.DirModuleSource{OriginalContextDir: directory}
	entrySrc.Kind = core.ModuleSourceKindDir

	entrySrcResult, err := dagql.NewObjectResultForCurrentCall(ctx, dag, entrySrc)
	if err != nil {
		return dagql.ObjectResult[*core.ModuleSource]{}, dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("attach module entrypoint source: %w", err)
	}
	return entrySrcResult, workspace, nil
}

func isWorkspaceRelativeEntrypointSource(source string) bool {
	if filepath.IsAbs(source) || strings.Contains(source, ":") {
		return false
	}
	_, err := gitutil.ParseURL(source)
	return err != nil
}

const moduleEntrypointInterface = `interface ModuleEntrypoint {
  types(workspace: Workspace!): [TypeDef!]!
  call(
    workspace: Workspace!
    receiverType: String!
    receiverValue: JSON
    fnName: String!
    fnArgs: JSON!
  ): JSON!
}
`

func runEntrypointDir(ctx context.Context, sourceDir string) (dang.ValueScope, error) {
	dir, err := os.MkdirTemp("", "dagger-module-entrypoint-")
	if err != nil {
		return nil, fmt.Errorf("create module entrypoint directory: %w", err)
	}
	defer os.RemoveAll(dir)

	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		return nil, fmt.Errorf("read module entrypoint directory: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".dang" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(sourceDir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read module entrypoint file %q: %w", entry.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(dir, entry.Name()), data, 0o600); err != nil {
			return nil, fmt.Errorf("copy module entrypoint file %q: %w", entry.Name(), err)
		}
	}
	contractPath := filepath.Join(dir, "__module_entrypoint.dang")
	contract, err := os.OpenFile(contractPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("install module entrypoint interface: %w", err)
	}
	if _, err := contract.WriteString(moduleEntrypointInterface); err != nil {
		contract.Close()
		return nil, fmt.Errorf("write module entrypoint interface: %w", err)
	}
	if err := contract.Close(); err != nil {
		return nil, fmt.Errorf("close module entrypoint interface: %w", err)
	}
	return dang.RunDir(ctx, dir, false)
}

func findModuleEntrypoint(env dang.ValueScope) (string, error) {
	module, ok := dangEvalModule(env)
	if !ok {
		return "", fmt.Errorf("module entrypoint source did not create a Dang module")
	}
	entrypointInterface, found := module.NamedType(moduleEntrypointTypeName)
	if !found {
		return "", fmt.Errorf("module entrypoint interface is not available")
	}

	var matches []string
	for _, binding := range env.Bindings(dang.PublicVisibility) {
		if !isDangLocalValueBinding(env, binding.Key) {
			continue
		}
		constructor, ok := binding.Value.(*dang.ConstructorFunction)
		if !ok || !constructor.ObjectType.ImplementsInterface(entrypointInterface) {
			continue
		}
		args, ok := constructor.FnType.Arg().(*dang.RecordType)
		if !ok || len(args.Fields) != 0 {
			return "", fmt.Errorf("module entrypoint %q must have a zero-argument constructor", binding.Key)
		}
		matches = append(matches, binding.Key)
	}

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("module entrypoint source must define one type that implements %s", moduleEntrypointTypeName)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("module entrypoint source defines multiple %s types: %s", moduleEntrypointTypeName, strings.Join(matches, ", "))
	}
}

func callEntrypointMethod(
	ctx context.Context,
	env dang.ValueScope,
	entrypointName string,
	method string,
	moduleContext dagql.ObjectResult[*core.Module],
	args []*core.FunctionCallArgValue,
) (dang.Value, error) {
	return callDangFunction(ctx, env, &core.FunctionCall{
		Name:       method,
		ParentName: entrypointName,
		Parent:     core.JSON("{}"),
		InputArgs:  args,
	}, dangModule{
		name:         moduleContext.Self().Name(),
		originalName: moduleContext.Self().OriginalName,
	})
}

func workspaceCallArg(workspace dagql.ObjectResult[*core.Workspace]) (*core.FunctionCallArgValue, error) {
	id, err := workspace.ID()
	if err != nil {
		return nil, fmt.Errorf("get module workspace ID: %w", err)
	}
	encoded, err := id.Encode()
	if err != nil {
		return nil, fmt.Errorf("encode module workspace ID: %w", err)
	}
	return stringCallArg("workspace", encoded), nil
}

func jsonCallArg(name string, value core.JSON) *core.FunctionCallArgValue {
	return &core.FunctionCallArgValue{Name: name, Value: value}
}

func stringCallArg(name, value string) *core.FunctionCallArgValue {
	data, _ := json.Marshal(value)
	return jsonCallArg(name, core.JSON(data))
}

func jsonScalarCallArg(name string, value core.JSON) *core.FunctionCallArgValue {
	return stringCallArg(name, string(value))
}

func functionArgsJSON(args []*core.FunctionCallArgValue) (core.JSON, error) {
	values := make(map[string]json.RawMessage, len(args))
	for _, arg := range args {
		if arg == nil {
			continue
		}
		if !json.Valid(arg.Value) {
			return nil, fmt.Errorf("function argument %q is not valid JSON", arg.Name)
		}
		values[arg.Name] = json.RawMessage(arg.Value)
	}
	data, err := json.Marshal(values)
	if err != nil {
		return nil, fmt.Errorf("encode function arguments: %w", err)
	}
	return core.JSON(data), nil
}

func entrypointJSONResult(value dang.Value) ([]byte, error) {
	switch value := value.(type) {
	case dang.ScalarValue:
		if value.ScalarType == nil {
			return nil, fmt.Errorf("module entrypoint call returned an untyped scalar instead of JSON")
		}
		if value.ScalarType.Name() != "JSON" {
			return nil, fmt.Errorf("module entrypoint call returned scalar %q instead of JSON", value.ScalarType.Name())
		}
		if !json.Valid([]byte(value.Val)) {
			return nil, fmt.Errorf("module entrypoint call returned invalid JSON")
		}
		return []byte(value.Val), nil
	case dang.NullValue:
		return []byte("null"), nil
	default:
		return nil, fmt.Errorf("module entrypoint call returned %T instead of JSON", value)
	}
}

func loadEntrypointTypeDefs(
	ctx context.Context,
	dag *dagql.Server,
	value dang.Value,
) (dagql.ObjectResultArray[*core.TypeDef], error) {
	list, ok := value.(dang.ListValue)
	if !ok {
		return nil, fmt.Errorf("module entrypoint types returned %T instead of [TypeDef!]!", value)
	}
	typeDefs := make(dagql.ObjectResultArray[*core.TypeDef], 0, len(list.Elements))
	for i, element := range list.Elements {
		object, ok := element.(dang.GraphQLValue)
		if !ok || object.TypeName != "TypeDef" {
			return nil, fmt.Errorf("module entrypoint types element %d is %T instead of TypeDef", i, element)
		}
		encoded, err := object.ID(ctx)
		if err != nil {
			return nil, fmt.Errorf("get module entrypoint type %d ID: %w", i, err)
		}
		var id dagql.ID[*core.TypeDef]
		if err := id.Decode(encoded); err != nil {
			return nil, fmt.Errorf("decode module entrypoint type %d ID: %w", i, err)
		}
		typeDef, err := id.Load(ctx, dag)
		if err != nil {
			return nil, fmt.Errorf("load module entrypoint type %d: %w", i, err)
		}
		typeDefs = append(typeDefs, typeDef)
	}
	return typeDefs, nil
}

func validateEntrypointConstructors(typeDefs dagql.ObjectResultArray[*core.TypeDef]) error {
	var constructors []string
	for _, typeDef := range typeDefs {
		if typeDef.Self().Kind != core.TypeDefKindObject || !typeDef.Self().AsObject.Value.Self().Constructor.Valid {
			continue
		}
		constructors = append(constructors, typeDef.Self().AsObject.Value.Self().OriginalName)
	}
	if len(constructors) > 1 {
		return fmt.Errorf("multiple object constructors are not supported: %s", strings.Join(constructors, ", "))
	}
	return nil
}

func moduleFromEntrypointTypeDefs(
	ctx context.Context,
	dag *dagql.Server,
	typeDefs dagql.ObjectResultArray[*core.TypeDef],
) (dagql.ObjectResult[*core.Module], error) {
	selectors := []dagql.Selector{{Field: "module"}}
	for i, typeDef := range typeDefs {
		id, err := typeDef.ID()
		if err != nil {
			return dagql.ObjectResult[*core.Module]{}, fmt.Errorf("get module entrypoint type %d ID: %w", i, err)
		}
		typeDefID := dagql.NewID[*core.TypeDef](id)
		switch typeDef.Self().Kind {
		case core.TypeDefKindObject:
			selectors = append(selectors, dagql.Selector{Field: "withObject", Args: []dagql.NamedInput{{Name: "object", Value: typeDefID}}})
		case core.TypeDefKindInterface:
			selectors = append(selectors, dagql.Selector{Field: "withInterface", Args: []dagql.NamedInput{{Name: "iface", Value: typeDefID}}})
		case core.TypeDefKindEnum:
			selectors = append(selectors, dagql.Selector{Field: "withEnum", Args: []dagql.NamedInput{{Name: "enum", Value: typeDefID}}})
		default:
			return dagql.ObjectResult[*core.Module]{}, fmt.Errorf("module entrypoint type %d has unsupported kind %q", i, typeDef.Self().Kind)
		}
	}

	var module dagql.ObjectResult[*core.Module]
	if err := dag.Select(ctx, dag.Root(), &module, selectors...); err != nil {
		return module, fmt.Errorf("create module from entrypoint types: %w", err)
	}
	return module, nil
}

var _ core.ModuleRuntime = (*entrypointRuntime)(nil)
