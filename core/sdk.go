package core

import (
	"context"
	"fmt"
	"strings"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/vektah/gqlparser/v2/ast"
)

const (
	runtimeWorkdirPath = "/scratch"
)

type SDKRuntime interface {
	/* Runtime returns a container that is used to execute module code at runtime in the Dagger engine.

	The provided Module is not fully initialized; the Runtime field will not be set yet.
	*/
	Runtime(context.Context, *ModDeps, dagql.Instance[*ModuleSource]) (*Container, error)
}

type SDKCodegen interface {
	/* Codegen generates code for the module at the given source directory and subpath.

	The Code field of the returned GeneratedCode object should be the generated contents of the module sourceDirSubpath,
	in the case where that's different than the root of the sourceDir.

	The provided Module is not fully initialized; the Runtime field will not be set yet.
	*/
	Codegen(context.Context, *ModDeps, dagql.Instance[*ModuleSource]) (*GeneratedCode, error)
}

/*
An SDK is an implementation of the functionality needed to generate code for and execute a module.

There is one special SDK, the Go SDK, which is implemented in `goSDK` below. It's used as the "seed" for all
other SDK implementations.

All other SDKs are themselves implemented as Modules, with Functions matching the two defined in this SDK interface.

An SDK Module needs to choose its own SDK for its implementation. This can be "well-known" built-in SDKs like "go",
"python", etc. Or it can be any external module as specified with a module source ref string.

You can thus think of SDK Modules as a DAG of dependencies, with each SDK using a different SDK to implement its Module,
with the Go SDK as the root of the DAG and the only one without any dependencies.

Built-in SDKs are also a bit special in that they come bundled w/ the engine container image, which allows them
to be used without hard dependencies on the internet. They are loaded w/ the `loadBuiltinSDK` function below, which
loads them as modules from the engine container.
*/
type SDK interface {
	AsRuntime(context.Context) (SDKRuntime, bool, error)
	AsCodegen(context.Context) (SDKCodegen, bool, error)
}

// ModuleSDKCapabilities defines the capabilities of the SDK used
// by the module.
// And can be used to enfore what capabilities are required to execute
// a specific function.
// For example, `dagger call` & `dagger functions` requires the SDK to
// implements the Runtime interface to work.
type ModuleSDKCapability string

var ModuleSDKCapabilities = dagql.NewEnum[ModuleSDKCapability]()

var (
	ModuleSDKCapabilityRuntime = ModuleSDKCapabilities.Register("RUNTIME")
	ModuleSDKCapabilityCodegen = ModuleSDKCapabilities.Register("CODEGEN")
)

func (capability ModuleSDKCapability) Type() *ast.Type {
	return &ast.Type{
		NamedType: "ModuleSDKCapability",
		NonNull:   true,
	}
}

func (capability ModuleSDKCapability) TypeDescription() string {
	return "Capabilities of the SDK used by the module."
}

func (capability ModuleSDKCapability) Decoder() dagql.InputDecoder {
	return ModuleSDKCapabilities
}

func (capability ModuleSDKCapability) ToLiteral() call.Literal {
	return ModuleSDKCapabilities.Literal(capability)
}

func (capability ModuleSDKCapability) Capability() string {
	return strings.ToLower(string(capability))
}

// Returns true if the given capabilities include the capability.
func (capability ModuleSDKCapability) IncludedIn(capabilities []ModuleSDKCapability) bool {
	for _, cap := range capabilities {
		if cap == capability {
			return true
		}
	}

	return false
}

// moduleSDK is an SDK implemented as module; i.e. every module besides the special case go sdk.
type moduleSDK struct {
	// The module implementing this SDK.
	mod dagql.Instance[*Module]
	// A server that the SDK module has been installed to.
	dag *dagql.Server
	// The SDK object retrieved from the server, for calling functions against.
	sdk dagql.Object
	// The main object of the sdk module for introspecting its implemented functions.
	mainObject *ObjectTypeDef
}

func NewModuleSDK(
	ctx context.Context,
	root *Query,
	sdkModule dagql.Instance[*Module],
	optionalFullSDKSourceDir dagql.Instance[*Directory],
) (SDK, error) {
	dag := dagql.NewServer(root)

	var err error
	dag.Cache, err = root.Cache(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get cache for sdk module %s: %w", sdkModule.Self.Name(), err)
	}
	dag.Around(AroundFunc)

	if err := sdkModule.Self.Install(ctx, dag); err != nil {
		return nil, fmt.Errorf("failed to install sdk module %s: %w", sdkModule.Self.Name(), err)
	}
	defaultDeps, err := sdkModule.Self.Query.DefaultDeps(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get default deps for sdk module %s: %w", sdkModule.Self.Name(), err)
	}
	for _, defaultDep := range defaultDeps.Mods {
		if err := defaultDep.Install(ctx, dag); err != nil {
			return nil, fmt.Errorf("failed to install default dep %s for sdk module %s: %w", defaultDep.Name(), sdkModule.Self.Name(), err)
		}
	}

	var sdk dagql.Object
	var constructorArgs []dagql.NamedInput
	if optionalFullSDKSourceDir.Self != nil {
		constructorArgs = []dagql.NamedInput{
			{Name: "sdkSourceDir", Value: dagql.Opt(dagql.NewID[*Directory](optionalFullSDKSourceDir.ID()))},
		}
	}
	if err := dag.Select(ctx, dag.Root(), &sdk,
		dagql.Selector{
			Field: gqlFieldName(sdkModule.Self.Name()),
			Args:  constructorArgs,
		},
	); err != nil {
		return nil, fmt.Errorf("failed to get sdk object for sdk module %s: %w", sdkModule.Self.Name(), err)
	}

	mainObject, err := findMainObjectInModule(sdkModule.Self)
	if err != nil {
		return nil, fmt.Errorf("failed to find main object in sdk module %s: %w", sdkModule.Self.Name(), err)
	}

	return &moduleSDK{mod: sdkModule, dag: dag, sdk: sdk, mainObject: mainObject}, nil
}

func findMainObjectInModule(mod *Module) (*ObjectTypeDef, error) {
	for _, obj := range mod.ObjectDefs {
		if gqlFieldName(obj.AsObject.Value.Name) == gqlFieldName(mod.Name()) {
			return obj.AsObject.Value, nil
		}
	}

	return nil, fmt.Errorf("no main object found for module %s", mod.Name())
}

func (m *moduleSDK) isImplementing(methods ...string) bool {
	nbImplemented := 0

	for _, fn := range m.mainObject.Functions {
		for _, method := range methods {
			if fn.Name == method {
				nbImplemented += 1
			}
		}
	}

	return nbImplemented == len(methods)
}

func (m *moduleSDK) AsCodegen(ctx context.Context) (SDKCodegen, bool, error) {
	if !m.isImplementing("codegen") {
		return nil, false, nil
	}

	return &codegen{sdk: m}, true, nil
}

func (m *moduleSDK) AsRuntime(ctx context.Context) (SDKRuntime, bool, error) {
	if !m.isImplementing("moduleRuntime") {
		return nil, false, nil
	}

	return &runtime{sdk: m}, true, nil
}

type runtime struct {
	sdk *moduleSDK
}

func (r *runtime) Runtime(
	ctx context.Context,
	deps *ModDeps,
	source dagql.Instance[*ModuleSource],
) (_ *Container, rerr error) {
	ctx, span := Tracer(ctx).Start(ctx, "module SDK: load runtime")
	defer telemetry.End(span, func() error { return rerr })
	schemaJSONFile, err := deps.SchemaIntrospectionJSONFile(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get schema introspection json during %s module sdk runtime: %w", r.sdk.mod.Self.Name(), err)
	}

	var inst dagql.Instance[*Container]
	err = r.sdk.dag.Select(ctx, r.sdk.sdk, &inst,
		dagql.Selector{
			Field: "moduleRuntime",
			Args: []dagql.NamedInput{
				{
					Name:  "modSource",
					Value: dagql.NewID[*ModuleSource](source.ID()),
				},
				{
					Name:  "introspectionJson",
					Value: dagql.NewID[*File](schemaJSONFile.ID()),
				},
			},
		},
		dagql.Selector{
			Field: "withWorkdir",
			Args: []dagql.NamedInput{
				{
					Name:  "path",
					Value: dagql.NewString(runtimeWorkdirPath),
				},
			},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to call sdk module moduleRuntime: %w", err)
	}
	return inst.Self, nil
}

type codegen struct {
	sdk *moduleSDK
}

func (c *codegen) Codegen(ctx context.Context, deps *ModDeps, source dagql.Instance[*ModuleSource]) (_ *GeneratedCode, rerr error) {
	ctx, span := Tracer(ctx).Start(ctx, "module SDK: run codegen")
	defer telemetry.End(span, func() error { return rerr })
	schemaJSONFile, err := deps.SchemaIntrospectionJSONFile(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get schema introspection json during %s module sdk codegen: %w", c.sdk.mod.Self.Name(), err)
	}

	var inst dagql.Instance[*GeneratedCode]
	err = c.sdk.dag.Select(ctx, c.sdk.sdk, &inst, dagql.Selector{
		Field: "codegen",
		Args: []dagql.NamedInput{
			{
				Name:  "modSource",
				Value: dagql.NewID[*ModuleSource](source.ID()),
			},
			{
				Name:  "introspectionJson",
				Value: dagql.NewID[*File](schemaJSONFile.ID()),
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to call sdk module codegen: %w", err)
	}
	return inst.Self, nil
}
