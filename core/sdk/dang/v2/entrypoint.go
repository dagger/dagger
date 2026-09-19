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
	"github.com/dagger/dagger/core/sdk/entrypoint"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/engineutil"
	telemetry "github.com/dagger/otel-go"
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
	entrySrc, workspace, err := entrypoint.ResolveSource(ctx, dag, src)
	if err != nil {
		return inst, err
	}
	schemaJSONFile, err := deps.SchemaIntrospectionJSONFileForModule(ctx)
	if err != nil {
		return inst, fmt.Errorf("get entrypoint schema: %w", err)
	}
	nestedClientMetadata, err := dangshared.NewNestedClientMetadata(ctx)
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
		true, /* inert attachables */
		nil,
		moduleContext,
		runEntrypointDir,
		func(ctx context.Context, env dang.ValueScope) ([]byte, error) {
			entrypointName, err := findModuleEntrypoint(env)
			if err != nil {
				return nil, err
			}
			workspaceArg, err := entrypoint.WorkspaceCallArg(workspace)
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
	if err := entrypoint.ValidateConstructors(typeDefs); err != nil {
		return inst, err
	}
	return entrypoint.ModuleFromTypeDefs(ctx, dag, typeDefs)
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
	entrySrc, workspace, err := entrypoint.ResolveSource(ctx, dag, r.modSource)
	if err != nil {
		return err
	}
	schemaJSONFile, err := r.deps.SchemaIntrospectionJSONFileForModule(ctx)
	if err != nil {
		return fmt.Errorf("get entrypoint schema: %w", err)
	}
	nestedClientMetadata, err := dangshared.NewNestedClientMetadata(ctx)
	if err != nil {
		return err
	}

	ctx, span := core.Tracer(ctx).Start(ctx, "call module entrypoint", telemetry.Encapsulate())
	defer telemetry.EndWithCause(span, &rerr)

	var resultJSON []byte
	_, err = evalDangSource(
		ctx,
		query,
		entrySrc,
		schemaJSONFile,
		nestedClientMetadata,
		true, /* inert attachables */
		fnCall,
		moduleContext,
		runEntrypointDir,
		func(ctx context.Context, env dang.ValueScope) ([]byte, error) {
			entrypointName, err := findModuleEntrypoint(env)
			if err != nil {
				return nil, err
			}
			workspaceArg, err := entrypoint.WorkspaceCallArg(workspace)
			if err != nil {
				return nil, err
			}
			fnArgs, err := entrypoint.FunctionArgsJSON(fnCall.InputArgs)
			if err != nil {
				return nil, err
			}
			result, err := callEntrypointMethod(ctx, env, entrypointName, "call", moduleContext, []*core.FunctionCallArgValue{
				workspaceArg,
				entrypoint.StringCallArg("receiverType", fnCall.ParentName),
				entrypoint.JSONScalarCallArg("receiverValue", fnCall.Parent),
				entrypoint.StringCallArg("fnName", fnCall.Name),
				entrypoint.JSONScalarCallArg("fnArgs", fnArgs),
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
		return nil, fmt.Errorf("module entrypoint types returned %T instead of a [TypeDef!]! list", value)
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

var _ core.ModuleRuntime = (*entrypointRuntime)(nil)
