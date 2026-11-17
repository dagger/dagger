package dangv2

import (
	"context"
	"crypto/rand"
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
	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/dagger/dagger/util/hashutil"
	telemetry "github.com/dagger/otel-go"
	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/hm"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
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
	entrySrc, err := entrypoint.ResolveSource(ctx, dag, src)
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
	nestedClientMetadata.SDKEntrypoint = true

	contract := entrypointContract{dangVersion: src.Self().DangVersion}
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
		false, /* the entrypoint's own types() builds the typedefs */
		contract.run,
		func(ctx context.Context, env dang.ValueScope) ([]byte, error) {
			entrypointName, err := findModuleEntrypoint(env)
			if err != nil {
				return nil, err
			}
			if contract.sources {
				// The engine evaluates sources while the module source loads,
				// which it does only for an entrypoint inside the module.
				local, err := entrypoint.IsLocalSource(ctx, src.Self())
				if err != nil {
					return nil, err
				}
				if !local {
					return nil, fmt.Errorf("%s needs a local entrypoint: entrypoint.source %q is not a path inside the module", EntrypointSourcesMember, src.Self().Entrypoint.Source)
				}
			}
			args, err := contract.workspaceArgs(ctx, dag, src)
			if err != nil {
				return nil, err
			}
			result, err := callEntrypointMethod(ctx, env, entrypointName, "types", moduleContext, args)
			if err != nil {
				return nil, fmt.Errorf("call module entrypoint types: %w", err)
			}
			typeDefs, err = loadEntrypointTypeDefs(ctx, dag, result)
			return nil, err
		},
	)
	if err != nil {
		return inst, convertError(err)
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
	execMD *engineutil.ExecutionMetadata,
	fnCall *core.FunctionCall,
	moduleContext dagql.ObjectResult[*core.Module],
) (rerr error) {
	defer func() {
		if rerr != nil {
			rerr = convertError(rerr)
		}
	}()

	fnCall.SetModuleProcess()
	fnCall.SetProcessSpanContext(dagql.UserFacingSpanContext(ctx))

	ctx, span := core.Tracer(ctx).Start(ctx, "call module entrypoint", telemetry.Internal(), telemetry.Encapsulate(),
		trace.WithAttributes(attribute.Bool(telemetryattrs.UIFunctionPlumbingAttr, true)))
	defer telemetry.EndWithCause(span, &rerr)
	fnCall.SetPlumbingSpanContext(span.SpanContext())

	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return fmt.Errorf("get Dagger server for entrypoint call: %w", err)
	}
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return fmt.Errorf("get current query for entrypoint call: %w", err)
	}
	entrySrc, err := entrypoint.ResolveSource(ctx, dag, r.modSource)
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
	nestedClientMetadata.SDKEntrypoint = true

	contract := entrypointContract{dangVersion: r.modSource.Self().DangVersion}
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
		false, /* a function call reads no object directives */
		contract.run,
		func(ctx context.Context, env dang.ValueScope) ([]byte, error) {
			entrypointName, err := findModuleEntrypoint(env)
			if err != nil {
				return nil, err
			}
			callArgs, err := contract.workspaceArgs(ctx, dag, r.modSource)
			if err != nil {
				return nil, err
			}
			fnArgs, err := entrypoint.FunctionArgsJSON(fnCall.InputArgs)
			if err != nil {
				return nil, err
			}
			callArgs = append(callArgs,
				entrypoint.StringCallArg("receiverType", fnCall.ParentName),
				entrypoint.JSONScalarCallArg("receiverValue", fnCall.Parent),
				entrypoint.StringCallArg("fnName", fnCall.Name),
				entrypoint.JSONScalarCallArg("fnArgs", fnArgs),
			)
			// An exec that the entrypoint starts is cached by its own recipe,
			// which misses the call's implicit inputs, such as the clients the
			// workspace declares. An entrypoint keys its exec on the call by
			// declaring a nullable callDigest argument; one that does not
			// declare it never receives it. The recipe digest is stable, so
			// the exec answers again when dagql evaluates a call again, such
			// as after a prune; it already changes per call for never and per
			// session for per-session functions. A function with an explicit
			// TTL gets a new digest on every evaluation instead: its result
			// can expire before that TTL when a call with a shorter one
			// returned the same result. So it runs again whenever dagql
			// evaluates it, also after a pruned result or a failed attempt.
			if execMD != nil && execMD.CallDigest != "" {
				callDigest := execMD.CallDigest
				if fnCall.CacheTTLSeconds() > 0 {
					callDigest = hashutil.HashStrings(callDigest.String(), rand.Text())
				}
				callArgs = append(callArgs, entrypoint.StringCallArg("callDigest", callDigest.String()))
			}
			result, err := callEntrypointMethod(ctx, env, entrypointName, "call", moduleContext, callArgs)
			if err != nil {
				return nil, fmt.Errorf("call module entrypoint: %w", returnRaisedError(ctx, fnCall, err))
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

// EntrypointPrebuildMember is the optional entrypoint member that returns the
// file every call of the module builds on, such as the module's compiled
// binary. The engine calls it without arguments for its own calls, and with
// a platform, as a string such as "linux/arm64", for the calls of an engine
// of another platform, as an engine image of that platform ships: a member
// that takes no platform declares no file for another. Since the file
// doesn't depend on a call, the
// engine can build it ahead of the module's first call, and a call that
// builds on the same file then finds it built or joins its build.
const EntrypointPrebuildMember = "prebuild"

// EntrypointRuntimeImageMember is the optional entrypoint member that returns
// the image every call of the module runs its process in, which the call
// starts its container from. The engine calls it as it calls
// EntrypointPrebuildMember, with a platform for another platform's engine.
// What a fresh engine fetches before the module's first call, such as an
// engine image pre-warms, is then read from the module, not from its
// entrypoint's text.
const EntrypointRuntimeImageMember = "runtimeImage"

// EntrypointPrebuildFile returns the file of mod's built-in Dang module
// entrypoint's prebuild member, not yet built, or false when the entrypoint
// declares no such member. The entrypoint runs as the module's calls see it:
// currentModule is mod.
func EntrypointPrebuildFile(
	ctx context.Context,
	mod dagql.ObjectResult[*core.Module],
	platform *core.Platform,
) (file dagql.ObjectResult[*core.File], declared bool, rerr error) {
	declared, rerr = evalEntrypointMember(ctx, mod, EntrypointPrebuildMember, platform, func(ctx context.Context, dag *dagql.Server, value dang.Value) (err error) {
		file, err = loadEntrypointObject[*core.File](ctx, dag, EntrypointPrebuildMember, value)
		return err
	})
	return file, declared, rerr
}

// EntrypointRuntimeImage returns the container of mod's built-in Dang module
// entrypoint's runtimeImage member, not yet pulled, or false when the
// entrypoint declares no such member, as EntrypointPrebuildFile returns its
// prebuild file.
func EntrypointRuntimeImage(
	ctx context.Context,
	mod dagql.ObjectResult[*core.Module],
	platform *core.Platform,
) (image dagql.ObjectResult[*core.Container], declared bool, rerr error) {
	declared, rerr = evalEntrypointMember(ctx, mod, EntrypointRuntimeImageMember, platform, func(ctx context.Context, dag *dagql.Server, value dang.Value) (err error) {
		image, err = loadEntrypointObject[*core.Container](ctx, dag, EntrypointRuntimeImageMember, value)
		return err
	})
	return image, declared, rerr
}

// evalEntrypointMember calls member of mod's built-in Dang module entrypoint,
// a member that takes no arguments but an optional platform, and passes its
// value to load. It reports false, without calling load, when the entrypoint
// declares no such member. The entrypoint runs as the module's calls see it:
// currentModule is mod.
func evalEntrypointMember(
	ctx context.Context,
	mod dagql.ObjectResult[*core.Module],
	member string,
	platform *core.Platform,
	load func(context.Context, *dagql.Server, dang.Value) error,
) (declared bool, rerr error) {
	defer func() {
		if rerr != nil {
			rerr = convertError(rerr)
		}
	}()
	if !mod.Self().Source.Valid {
		return false, fmt.Errorf("module %q has no source", mod.Self().Name())
	}
	src := mod.Self().Source.Value
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return false, fmt.Errorf("get Dagger server for entrypoint %s: %w", member, err)
	}
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return false, fmt.Errorf("get current query for entrypoint %s: %w", member, err)
	}
	entrySrc, err := entrypoint.ResolveSource(ctx, dag, src)
	if err != nil {
		return false, err
	}
	schemaJSONFile, err := mod.Self().SDKDeps().SchemaIntrospectionJSONFileForModule(ctx)
	if err != nil {
		return false, fmt.Errorf("get entrypoint schema: %w", err)
	}
	nestedClientMetadata, err := dangshared.NewNestedClientMetadata(ctx)
	if err != nil {
		return false, err
	}
	nestedClientMetadata.SDKEntrypoint = true

	// The program runs as the module's process, as a call's does: an exec it
	// starts is keyed on the module it runs for, so the result it returns is
	// the one the module's calls build.
	fnCall := &core.FunctionCall{Name: member}
	fnCall.SetModuleProcess()

	contract := entrypointContract{dangVersion: src.Self().DangVersion, member: member}
	_, err = evalDangSource(
		ctx,
		query,
		entrySrc,
		schemaJSONFile,
		nestedClientMetadata,
		true, /* inert attachables */
		fnCall,
		mod,
		false, /* the member reads no object directives */
		contract.run,
		func(ctx context.Context, env dang.ValueScope) ([]byte, error) {
			if !contract.declaresMember {
				return nil, nil
			}
			declared = true
			entrypointName, err := findModuleEntrypoint(env)
			if err != nil {
				return nil, err
			}
			// Another platform's is the member's with that platform as its
			// argument; this engine's, the member's without one, as every
			// entrypoint that declares it takes it.
			var args []*core.FunctionCallArgValue
			if platform != nil {
				// A member that takes no platform answers for this engine's,
				// whatever it is given.
				takes, err := entrypointMemberTakes(ctx, env, entrypointName, member, "platform")
				if err != nil {
					return nil, err
				}
				if !takes {
					return nil, fmt.Errorf("module entrypoint %s takes no platform: it answers for this engine's platform only, not %s", member, platform.Format())
				}
				value, err := json.Marshal(platform.Format())
				if err != nil {
					return nil, err
				}
				args = append(args, &core.FunctionCallArgValue{Name: "platform", Value: core.JSON(value)})
			}
			value, err := callEntrypointMethod(ctx, env, entrypointName, member, mod, args)
			if err != nil {
				return nil, fmt.Errorf("call module entrypoint %s: %w", member, err)
			}
			return nil, load(ctx, dag, value)
		},
	)
	if err != nil {
		return false, err
	}
	return declared, nil
}

// loadEntrypointObject loads the object of type T that the entrypoint's
// member returned as value.
func loadEntrypointObject[T dagql.Typed](ctx context.Context, dag *dagql.Server, member string, value dang.Value) (dagql.ObjectResult[T], error) {
	var inst dagql.ObjectResult[T]
	var zero T
	typeName := zero.Type().Name()
	object, ok := value.(dang.GraphQLValue)
	if !ok || object.TypeName != typeName {
		return inst, fmt.Errorf("module entrypoint %s returned %T instead of a %s", member, value, typeName)
	}
	encoded, err := object.ID(ctx)
	if err != nil {
		return inst, fmt.Errorf("get module entrypoint %s %s ID: %w", member, typeName, err)
	}
	var id dagql.ID[T]
	if err := id.Decode(encoded); err != nil {
		return inst, fmt.Errorf("decode module entrypoint %s %s ID: %w", member, typeName, err)
	}
	return id.Load(ctx, dag)
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

// sourcesEntrypointInterface is the contract of an entrypoint that declares
// sources. types and call read the module through currentModule, whose context
// is what sources returned, so they get no workspace: a workspace read there
// would escape every cache key. Dang accepts an extra nullable argument, so an
// entrypoint that also runs on an engine with the other contract can keep a
// nullable workspace argument.
const sourcesEntrypointInterface = `interface ModuleEntrypoint {
  sources(workspace: Workspace!): Directory!
  types: [TypeDef!]!
  call(
    receiverType: String!
    receiverValue: JSON
    fnName: String!
    fnArgs: JSON!
  ): JSON!
}
`

// entrypointContract is stateful because the contract is known only once run
// reads the entrypoint, which evalDangSource mounts.
type entrypointContract struct {
	// dangVersion is the Dang version the entrypoint is written in
	// (ModuleSource.DangVersion).
	dangVersion string
	sources     bool
	// member is the optional entrypoint member the evaluation calls, if any,
	// and declaresMember whether the entrypoint declares it.
	member         string
	declaresMember bool
}

func (c *entrypointContract) run(ctx context.Context, sourceDir string) (dang.ValueScope, error) {
	// The sources check parses through the syntax cache at the version on
	// ctx, so set the entrypoint's version before it, not only for the run.
	ctx, err := contextWithEntrypointVersion(ctx, c.dangVersion)
	if err != nil {
		return nil, err
	}
	dir, err := copyEntrypointDir(sourceDir)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	if c.sources, err = declaresEntrypointMember(ctx, dir, EntrypointSourcesMember); err != nil {
		return nil, err
	}
	if c.member != "" {
		if c.declaresMember, err = declaresEntrypointMember(ctx, dir, c.member); err != nil {
			return nil, err
		}
	}
	return runCopiedEntrypoint(ctx, dir, c.sources)
}

func (c *entrypointContract) workspaceArgs(
	ctx context.Context,
	dag *dagql.Server,
	src dagql.ObjectResult[*core.ModuleSource],
) ([]*core.FunctionCallArgValue, error) {
	if c.sources {
		return nil, nil
	}
	workspace, err := entrypoint.Workspace(ctx, dag, src)
	if err != nil {
		return nil, err
	}
	arg, err := entrypoint.WorkspaceCallArg(workspace)
	if err != nil {
		return nil, err
	}
	return []*core.FunctionCallArgValue{arg}, nil
}

func runEntrypointDir(ctx context.Context, sourceDir string, dangVersion string, sources bool) (dang.ValueScope, error) {
	ctx, err := contextWithEntrypointVersion(ctx, dangVersion)
	if err != nil {
		return nil, err
	}
	dir, err := copyEntrypointDir(sourceDir)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	return runCopiedEntrypoint(ctx, dir, sources)
}

// copyEntrypointDir copies the entrypoint's top-level Dang files, and nothing
// else, to a new temporary directory, where the engine installs the
// entrypoint interface beside them and runs them. The caller removes it.
func copyEntrypointDir(sourceDir string) (_ string, rerr error) {
	dir, err := os.MkdirTemp("", "dagger-module-entrypoint-")
	if err != nil {
		return "", fmt.Errorf("create module entrypoint directory: %w", err)
	}
	defer func() {
		if rerr != nil {
			os.RemoveAll(dir)
		}
	}()
	files, err := entrypointFiles(sourceDir)
	if err != nil {
		return "", err
	}
	for _, path := range files {
		name := filepath.Base(path)
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read module entrypoint file %q: %w", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			return "", fmt.Errorf("copy module entrypoint file %q: %w", name, err)
		}
	}
	return dir, nil
}

// runCopiedEntrypoint installs the entrypoint interface in dir, a copy from
// copyEntrypointDir, and runs it.
func runCopiedEntrypoint(ctx context.Context, dir string, sources bool) (dang.ValueScope, error) {
	ctx = withDangSyntaxCache(ctx)
	contractPath := filepath.Join(dir, "__module_entrypoint.dang")
	contract, err := os.OpenFile(contractPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("install module entrypoint interface: %w", err)
	}
	iface := moduleEntrypointInterface
	if sources {
		iface = sourcesEntrypointInterface
	}
	if _, err := contract.WriteString(iface); err != nil {
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

// entrypointMemberTakes reports whether the entrypoint's member takes the
// argument arg.
func entrypointMemberTakes(ctx context.Context, env dang.ValueScope, entrypointName, member, arg string) (bool, error) {
	base, found, err := env.Lookup(ctx, entrypointName)
	if err != nil {
		return false, fmt.Errorf("lookup module entrypoint %s: %w", entrypointName, err)
	}
	constructor, ok := base.(*dang.ConstructorFunction)
	if !found || !ok {
		return false, fmt.Errorf("module entrypoint %s is not a type", entrypointName)
	}
	scheme, found := constructor.ObjectType.SchemeOf(member)
	if !found {
		return false, fmt.Errorf("module entrypoint %s has no member %s", entrypointName, member)
	}
	t, _ := scheme.Type()
	fn, ok := t.(*hm.FunctionType)
	if !ok {
		return false, nil
	}
	args, ok := fn.Arg().(*dang.RecordType)
	if !ok {
		return false, nil
	}
	for _, field := range args.Fields {
		if field.Key == arg {
			return true, nil
		}
	}
	return false, nil
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
		namer:        moduleContext.Self().Namer(),
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
