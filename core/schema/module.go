package schema

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/modules"
)

type moduleSchema struct {
	*APIServer
}

var _ SchemaResolvers = &moduleSchema{}

func (s *moduleSchema) Name() string {
	return "module"
}

func (s *moduleSchema) Schema() string {
	return strings.Join([]string{Module, TypeDef, InternalSDK}, "\n")
}

func (s *moduleSchema) Resolvers() Resolvers {
	rs := Resolvers{
		"Query": ObjectResolver{
			"module":              ToResolver(s.module),
			"currentModule":       ToResolver(s.currentModule),
			"function":            ToResolver(s.function),
			"currentFunctionCall": ToResolver(s.currentFunctionCall),
			"typeDef":             ToResolver(s.typeDef),
			"generatedCode":       ToResolver(s.generatedCode),
			"moduleConfig":        ToResolver(s.moduleConfig),
			"currentTypeDefs":     ToResolver(s.currentTypeDefs),
		},
		"Directory": ObjectResolver{
			"asModule": ToResolver(s.directoryAsModule),
		},
		"FunctionCall": ObjectResolver{
			"returnValue": ToVoidResolver(s.functionCallReturnValue),
			"parent":      ToResolver(s.functionCallParent),
		},
	}

	ResolveIDable[core.Module](rs, "Module", ObjectResolver{
		"dependencies":  ToResolver(s.moduleDependencies),
		"objects":       ToResolver(s.moduleObjects),
		"withObject":    ToResolver(s.moduleWithObject),
		"withInterface": ToResolver(s.moduleWithInterface),
		"generatedCode": ToResolver(s.moduleGeneratedCode),
		"serve":         ToVoidResolver(s.moduleServe),
	})

	ResolveIDable[core.Function](rs, "Function", ObjectResolver{
		"withDescription": ToResolver(s.functionWithDescription),
		"withArg":         ToResolver(s.functionWithArg),
	})

	ResolveIDable[core.FunctionArg](rs, "FunctionArg", ObjectResolver{})

	ResolveIDable[core.TypeDef](rs, "TypeDef", ObjectResolver{
		"kind":            ToResolver(s.typeDefKind),
		"withOptional":    ToResolver(s.typeDefWithOptional),
		"withKind":        ToResolver(s.typeDefWithKind),
		"withListOf":      ToResolver(s.typeDefWithListOf),
		"withObject":      ToResolver(s.typeDefWithObject),
		"withInterface":   ToResolver(s.typeDefWithInterface),
		"withField":       ToResolver(s.typeDefWithObjectField),
		"withFunction":    ToResolver(s.typeDefWithFunction),
		"withConstructor": ToResolver(s.typeDefWithObjectConstructor),
	})

	ResolveIDable[core.GeneratedCode](rs, "GeneratedCode", ObjectResolver{
		"withVCSIgnoredPaths":   ToResolver(s.generatedCodeWithVCSIgnoredPaths),
		"withVCSGeneratedPaths": ToResolver(s.generatedCodeWithVCSGeneratedPaths),
	})

	return rs
}

func (s *moduleSchema) typeDef(ctx context.Context, _ *core.Query, args struct {
	ID   core.TypeDefID
	Kind core.TypeDefKind
}) (*core.TypeDef, error) {
	if args.ID != "" {
		return args.ID.Decode()
	}
	return &core.TypeDef{
		Kind: args.Kind,
	}, nil
}

func (s *moduleSchema) typeDefWithOptional(ctx context.Context, def *core.TypeDef, args struct {
	Optional bool
}) (*core.TypeDef, error) {
	return def.WithOptional(args.Optional), nil
}

func (s *moduleSchema) typeDefWithKind(ctx context.Context, def *core.TypeDef, args struct {
	Kind core.TypeDefKind
}) (*core.TypeDef, error) {
	return def.WithKind(args.Kind), nil
}

func (s *moduleSchema) typeDefWithListOf(ctx context.Context, def *core.TypeDef, args struct {
	ElementType core.TypeDefID
}) (*core.TypeDef, error) {
	elemType, err := args.ElementType.Decode()
	if err != nil {
		return nil, fmt.Errorf("failed to decode element type: %w", err)
	}
	return def.WithListOf(elemType), nil
}

func (s *moduleSchema) typeDefWithObject(ctx context.Context, def *core.TypeDef, args struct {
	Name        string
	Description string
}) (*core.TypeDef, error) {
	return def.WithObject(args.Name, args.Description), nil
}

func (s *moduleSchema) typeDefWithInterface(ctx context.Context, def *core.TypeDef, args struct {
	Name        string
	Description string
}) (*core.TypeDef, error) {
	return def.WithInterface(args.Name, args.Description), nil
}

func (s *moduleSchema) typeDefWithObjectField(ctx context.Context, def *core.TypeDef, args struct {
	Name        string
	TypeDef     core.TypeDefID
	Description string
}) (*core.TypeDef, error) {
	fieldType, err := args.TypeDef.Decode()
	if err != nil {
		return nil, fmt.Errorf("failed to decode element type: %w", err)
	}
	return def.WithObjectField(args.Name, fieldType, args.Description)
}

func (s *moduleSchema) typeDefWithFunction(ctx context.Context, def *core.TypeDef, args struct {
	Function core.FunctionID
}) (*core.TypeDef, error) {
	fn, err := args.Function.Decode()
	if err != nil {
		return nil, fmt.Errorf("failed to decode element type: %w", err)
	}
	return def.WithFunction(fn)
}

func (s *moduleSchema) typeDefWithObjectConstructor(ctx context.Context, def *core.TypeDef, args struct {
	Function core.FunctionID
}) (*core.TypeDef, error) {
	fn, err := args.Function.Decode()
	if err != nil {
		return nil, fmt.Errorf("failed to decode element type: %w", err)
	}
	// Constructors are invoked by setting the ObjectName to the name of the object its constructing and the
	// FunctionName to "", so ignore the name of the function.
	fn.Name = ""
	fn.OriginalName = ""
	return def.WithObjectConstructor(fn)
}

func (s *moduleSchema) typeDefKind(ctx context.Context, def *core.TypeDef, args any) (string, error) {
	return def.Kind.String(), nil
}

func (s *moduleSchema) generatedCode(ctx context.Context, _ *core.Query, args struct {
	Code core.DirectoryID
}) (*core.GeneratedCode, error) {
	dir, err := args.Code.Decode()
	if err != nil {
		return nil, err
	}
	return core.NewGeneratedCode(dir), nil
}

func (s *moduleSchema) generatedCodeWithVCSIgnoredPaths(ctx context.Context, code *core.GeneratedCode, args struct {
	Paths []string
}) (*core.GeneratedCode, error) {
	return code.WithVCSIgnoredPaths(args.Paths), nil
}

func (s *moduleSchema) generatedCodeWithVCSGeneratedPaths(ctx context.Context, code *core.GeneratedCode, args struct {
	Paths []string
}) (*core.GeneratedCode, error) {
	return code.WithVCSGeneratedPaths(args.Paths), nil
}

func (s *moduleSchema) module(ctx context.Context, query *core.Query, _ any) (*core.Module, error) {
	return &core.Module{}, nil
}

type moduleConfigArgs struct {
	SourceDirectory core.DirectoryID
	Subpath         string
}

func (s *moduleSchema) moduleConfig(ctx context.Context, query *core.Query, args moduleConfigArgs) (*modules.Config, error) {
	srcDir, err := args.SourceDirectory.Decode()
	if err != nil {
		return nil, fmt.Errorf("failed to decode source directory: %w", err)
	}

	_, cfg, err := core.LoadModuleConfig(ctx, s.bk, s.services, srcDir, args.Subpath)
	return cfg, err
}

func (s *moduleSchema) function(ctx context.Context, _ *core.Query, args struct {
	Name       string
	ReturnType core.TypeDefID
}) (*core.Function, error) {
	returnType, err := args.ReturnType.Decode()
	if err != nil {
		return nil, fmt.Errorf("failed to decode return type: %w", err)
	}
	return core.NewFunction(args.Name, returnType), nil
}

func (s *moduleSchema) functionWithDescription(ctx context.Context, fn *core.Function, args struct {
	Description string
}) (*core.Function, error) {
	return fn.WithDescription(args.Description), nil
}

func (s *moduleSchema) functionWithArg(ctx context.Context, fn *core.Function, args struct {
	Name         string
	TypeDef      core.TypeDefID
	Description  string
	DefaultValue any
}) (*core.Function, error) {
	argType, err := args.TypeDef.Decode()
	if err != nil {
		return nil, fmt.Errorf("failed to decode arg type: %w", err)
	}
	return fn.WithArg(args.Name, argType, args.Description, args.DefaultValue), nil
}

func (s *moduleSchema) functionCallParent(ctx context.Context, fnCall *core.FunctionCall, _ any) (any, error) {
	if fnCall.Parent == nil {
		return struct{}{}, nil
	}
	return fnCall.Parent, nil
}

type asModuleArgs struct {
	SourceSubpath string
}

func (s *moduleSchema) directoryAsModule(ctx context.Context, sourceDir *core.Directory, args asModuleArgs) (_ *core.Module, rerr error) {
	modMeta, err := core.ModuleFromConfig(ctx, s.bk, s.services, sourceDir, args.SourceSubpath)
	if err != nil {
		return nil, fmt.Errorf("failed to create module from config: %w", err)
	}

	mod, err := s.GetOrAddModFromMetadata(ctx, modMeta, sourceDir.PipelinePath())
	if err != nil {
		return nil, fmt.Errorf("failed to add module to dag: %w", err)
	}

	return mod.metadata, nil
}

func (s *moduleSchema) moduleObjects(ctx context.Context, modMeta *core.Module, _ any) ([]*core.TypeDef, error) {
	mod, err := s.GetOrAddModFromMetadata(ctx, modMeta, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get module: %w", err)
	}
	objs, err := mod.Objects(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get module objects: %w", err)
	}
	typeDefs := make([]*core.TypeDef, 0, len(objs))
	for _, obj := range objs {
		typeDefs = append(typeDefs, obj.typeDef)
	}
	return typeDefs, nil
}

func (s *moduleSchema) currentModule(ctx context.Context, _, _ any) (*core.Module, error) {
	mod, err := s.APIServer.CurrentModule(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get current module: %w", err)
	}
	return mod.metadata, nil
}

func (s *moduleSchema) currentTypeDefs(ctx context.Context, _, _ any) ([]*core.TypeDef, error) {
	deps, err := s.APIServer.CurrentServedDeps(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get current module: %w", err)
	}
	return deps.TypeDefs(ctx)
}

func (s *moduleSchema) currentFunctionCall(ctx context.Context, _ *core.Query, _ any) (*core.FunctionCall, error) {
	return s.APIServer.CurrentFunctionCall(ctx)
}

func (s *moduleSchema) moduleServe(ctx context.Context, modMeta *core.Module, _ any) error {
	return s.APIServer.ServeModuleToMainClient(ctx, modMeta)
}

func (s *moduleSchema) functionCallReturnValue(ctx context.Context, fnCall *core.FunctionCall, args struct{ Value any }) error {
	// TODO: error out if caller is not coming from a module

	valueBytes, err := json.Marshal(args.Value)
	if err != nil {
		return fmt.Errorf("failed to marshal function return value: %w", err)
	}

	// The return is implemented by exporting the result back to the caller's filesystem. This ensures that
	// the result is cached as part of the module function's Exec while also keeping SDKs as agnostic as possible
	// to the format + location of that result.
	return s.bk.IOReaderExport(ctx, bytes.NewReader(valueBytes), filepath.Join(modMetaDirPath, modMetaOutputPath), 0600)
}

func (s *moduleSchema) moduleWithObject(ctx context.Context, modMeta *core.Module, args struct {
	Object core.TypeDefID
}) (_ *core.Module, rerr error) {
	def, err := args.Object.Decode()
	if err != nil {
		return nil, err
	}
	return modMeta.WithObject(def)
}

func (s *moduleSchema) moduleWithInterface(ctx context.Context, modMeta *core.Module, args struct {
	Iface core.TypeDefID
}) (_ *core.Module, rerr error) {
	def, err := args.Iface.Decode()
	if err != nil {
		return nil, err
	}
	return modMeta.WithInterface(def)
}

func (s *moduleSchema) moduleDependencies(ctx context.Context, modMeta *core.Module, _ any) ([]*core.Module, error) {
	mod, err := s.GetOrAddModFromMetadata(ctx, modMeta, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get module: %w", err)
	}
	modMetas := make([]*core.Module, 0, len(mod.Dependencies()))
	for _, dep := range mod.Dependencies() {
		// only include user modules, not core
		userMod, ok := dep.(*UserMod)
		if !ok {
			continue
		}
		modMetas = append(modMetas, userMod.metadata)
	}
	return modMetas, nil
}

func (s *moduleSchema) moduleGeneratedCode(ctx context.Context, modMeta *core.Module, _ any) (*core.GeneratedCode, error) {
	mod, err := s.GetOrAddModFromMetadata(ctx, modMeta, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get module: %w", err)
	}
	return mod.Codegen(ctx)
}
