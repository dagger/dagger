package schema

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/vito/progrock"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/internal/distconsts"
)

/*
An SDK is an implementation of the functionality needed to generate code for and execute a module.

There is one special SDK, the Go SDK, which is implemented in `goSDK` below. It's used as the "seed" for all
other SDK implementations.

All other SDKs are themselves implemented as Modules, with Functions matching the two defined in this SDK interface.

An SDK Module needs to choose its own SDK for its implementation. This can be "well-known" built-in SDKs like "go",
"python", etc. Or it can be any external module as specified with a module ref.

You can thus think of SDK Modules as a DAG of dependencies, with each SDK using a different SDK to implement its Module,
with the Go SDK as the root of the DAG and the only one without any dependencies.

Built-in SDKs are also a bit special in that they come bundled w/ the engine container image, which allows them
to be used without hard dependencies on the internet. They are loaded w/ the `loadBuiltinSDK` function below, which
loads them as modules from the engine container.
*/
type SDK interface {
	/* Codegen generates code for the module at the given source directory and subpath.

	The Code field of the returned GeneratedCode object should be the generated contents of the module sourceDirSubpath,
	in the case where that's different than the root of the sourceDir.

	The provided Module is not fully initialized; the Runtime field will not be set yet.
	*/
	Codegen(ctx context.Context, mod *UserMod) (*core.GeneratedCode, error)

	/* Runtime returns a container that is used to execute module code at runtime in the Dagger engine.

	The provided Module is not fully initialized; the Runtime field will not be set yet.
	*/
	Runtime(ctx context.Context, mod *UserMod) (*core.Container, error)
}

// load the SDK implementation with the given name for the module at the given source dir + subpath.
func (s *APIServer) sdkForModule(ctx context.Context, mod *core.Module) (SDK, error) {
	builtinSDK, err := s.builtinSDK(ctx, mod.SDK)
	if err == nil {
		return builtinSDK, nil
	} else if !errors.Is(err, errUnknownBuiltinSDK) {
		return nil, err
	}

	sdkMod, err := core.ModuleFromRef(ctx, s.bk, s.services, nil, s.platform,
		mod.SourceDirectory, mod.SourceDirectorySubpath,
		mod.SDK,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load sdk module %s: %w", mod.SDK, err)
	}

	return s.newModuleSDK(ctx, sdkMod)
}

var errUnknownBuiltinSDK = fmt.Errorf("unknown builtin sdk")

// return a builtin SDK implementation with the given name
func (s *APIServer) builtinSDK(ctx context.Context, sdkName string) (SDK, error) {
	switch sdkName {
	case "go":
		return &goSDK{APIServer: s}, nil
	case "python":
		return s.loadBuiltinSDK(ctx, sdkName, distconsts.PythonSDKEngineContainerModulePath)
	case "typescript":
		return s.loadBuiltinSDK(ctx, sdkName, distconsts.TypescriptSDKEngineContainerModulePath)
	default:
		return nil, fmt.Errorf("%s: %w", sdkName, errUnknownBuiltinSDK)
	}
}

// moduleSDK is an SDK implemented as module; i.e. every module besides the special case go sdk.
type moduleSDK struct {
	*APIServer
	// The module implementing this SDK.
	mod *UserMod
}

func (s *APIServer) newModuleSDK(ctx context.Context, sdkModMeta *core.Module) (*moduleSDK, error) {
	sdkMod, err := s.GetOrAddModFromMetadata(ctx, sdkModMeta, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to add sdk module to dag: %w", err)
	}
	return &moduleSDK{APIServer: s, mod: sdkMod}, nil
}

// Codegen calls the Codegen function on the SDK Module
//
//nolint:dupl
func (sdk *moduleSDK) Codegen(ctx context.Context, mod *UserMod) (*core.GeneratedCode, error) {
	mainModObj, err := sdk.mod.MainModuleObject(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get main module object for sdk module %s: %w", sdk.mod.Name(), err)
	}
	codegenFn, ok, err := mainModObj.FunctionByName(ctx, "Codegen")
	if err != nil {
		return nil, fmt.Errorf("failed to get Codegen function in SDK module %s: %w", sdk.mod.Name(), err)
	}
	if !ok {
		return nil, fmt.Errorf("failed to find required Codegen function in SDK module %s: %w", sdk.mod.Name(), err)
	}

	introspectionJSON, err := mod.DependencySchemaIntrospectionJSON(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get schema introspection json during %s module sdk codegen: %w", sdk.mod.Name(), err)
	}

	srcDirID, err := mod.metadata.SourceDirectory.ID()
	if err != nil {
		return nil, fmt.Errorf("failed to get source directory id: %w", err)
	}

	result, err := codegenFn.Call(ctx, &CallOpts{
		Cache: true,
		Inputs: []*core.CallInput{
			{
				Name:  "modSource",
				Value: srcDirID,
			},
			{
				Name:  "subPath",
				Value: mod.metadata.SourceDirectorySubpath,
			},
			{
				Name:  "introspectionJson",
				Value: introspectionJSON,
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to call sdk module: %w", err)
	}

	genCode, ok := result.(*core.GeneratedCode)
	if !ok {
		return nil, fmt.Errorf("expected generated code result, got %T", result)
	}
	return genCode, nil
}

// Runtime calls the Runtime function on the SDK Module
//
//nolint:dupl
func (sdk *moduleSDK) Runtime(ctx context.Context, mod *UserMod) (*core.Container, error) {
	mainModObj, err := sdk.mod.MainModuleObject(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get main module object for sdk module %s: %w", sdk.mod.Name(), err)
	}
	getRuntimeFn, ok, err := mainModObj.FunctionByName(ctx, "ModuleRuntime")
	if err != nil {
		return nil, fmt.Errorf("failed to get ModuleRuntime function in SDK module %s: %w", sdk.mod.Name(), err)
	}
	if !ok {
		return nil, fmt.Errorf("failed to find required ModuleRuntime function in SDK module %s: %w", sdk.mod.Name(), err)
	}

	introspectionJSON, err := mod.DependencySchemaIntrospectionJSON(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get schema introspection json during %s module sdk codegen: %w", sdk.mod.Name(), err)
	}

	srcDirID, err := mod.metadata.SourceDirectory.ID()
	if err != nil {
		return nil, fmt.Errorf("failed to get source directory id: %w", err)
	}

	result, err := getRuntimeFn.Call(ctx, &CallOpts{
		Cache: true,
		Inputs: []*core.CallInput{
			{
				Name:  "modSource",
				Value: srcDirID,
			},
			{
				Name:  "subPath",
				Value: mod.metadata.SourceDirectorySubpath,
			},
			{
				Name:  "introspectionJson",
				Value: introspectionJSON,
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to call sdk module: %w", err)
	}

	runtime, ok := result.(*core.Container)
	if !ok {
		return nil, fmt.Errorf("expected container runtime result, got %T", result)
	}

	return runtime, nil
}

// loadBuiltinSDK loads an SDK implemented as a module that is "builtin" to engine, which means its pre-packaged
// with the engine container in order to enable use w/out hard dependencies on the internet
func (s *APIServer) loadBuiltinSDK(ctx context.Context, name string, engineContainerModulePath string) (*moduleSDK, error) {
	ctx, recorder := progrock.WithGroup(ctx, fmt.Sprintf("load builtin module sdk %s", name))

	cfgPath := modules.NormalizeConfigPath(engineContainerModulePath)
	cfgPBDef, err := s.bk.EngineContainerLocalImport(
		ctx,
		recorder,
		s.platform,
		filepath.Dir(cfgPath),
		nil,
		[]string{filepath.Base(cfgPath)},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to import module sdk config file %s from engine container filesystem: %w", name, err)
	}

	cfgFile := core.NewFile(ctx, cfgPBDef, filepath.Base(cfgPath), nil, s.platform, nil)
	modCfg, err := core.LoadModuleConfigFromFile(ctx, s.bk, s.services, cfgFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load module sdk config file %s: %w", name, err)
	}

	modRootPath := filepath.Join(filepath.Dir(cfgPath), modCfg.Root)
	pbDef, err := s.bk.EngineContainerLocalImport(
		ctx,
		recorder,
		s.platform,
		modRootPath,
		modCfg.Exclude,
		modCfg.Include,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to import module sdk %s from engine container filesystem: %w", name, err)
	}

	cfgRelPath, err := filepath.Rel(modRootPath, cfgPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get relative path of module sdk config file %s: %w", name, err)
	}

	sdkMod, err := core.ModuleFromConfig(ctx, s.bk, s.services,
		core.NewDirectory(ctx, pbDef, "/", nil, s.platform, nil),
		cfgRelPath,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load sdk module: %w", err)
	}

	return s.newModuleSDK(ctx, sdkMod)
}

const (
	goSDKUserModSourceDirPath  = "/src"
	goSDKRuntimePath           = "/runtime"
	goSDKIntrospectionJSONPath = "/schema.json"
)

/*
	goSDK is the one special sdk not implemented as module, instead the `cmd/codegen/` binary is packaged into

a container w/ the go runtime, tarball'd up and included in the engine image.

The Codegen and Runtime methods are implemented by loading that tarball and executing the codegen binary inside it
to generate user code and then execute it with the resulting /runtime binary.
*/
type goSDK struct {
	*APIServer
}

func (sdk *goSDK) Codegen(ctx context.Context, mod *UserMod) (*core.GeneratedCode, error) {
	ctr, err := sdk.baseWithCodegen(ctx, mod)
	if err != nil {
		return nil, err
	}

	modifiedSrcDir, err := ctr.Directory(ctx, sdk.bk, sdk.services, goSDKUserModSourceDirPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get modified source directory for go module sdk codegen: %w", err)
	}

	diff, err := mod.metadata.SourceDirectory.Diff(ctx, modifiedSrcDir)
	if err != nil {
		return nil, fmt.Errorf("failed to get diff: %w", err)
	}
	// diff needs to be of the subdir, if any, not necessarily the root
	diff, err = diff.Directory(ctx, sdk.bk, sdk.services, mod.metadata.SourceDirectorySubpath)
	if err != nil {
		return nil, fmt.Errorf("failed to re-root diff: %w", err)
	}

	return &core.GeneratedCode{
		Code: diff,
		VCSIgnoredPaths: []string{
			"dagger.gen.go",
			"internal/querybuilder/",
			"querybuilder/", // for old repos
		},
	}, nil
}

func (sdk *goSDK) Runtime(ctx context.Context, mod *UserMod) (*core.Container, error) {
	ctr, err := sdk.baseWithCodegen(ctx, mod)
	if err != nil {
		return nil, err
	}

	ctr, err = ctr.WithExec(ctx, sdk.bk, sdk.progSockPath, sdk.platform, core.ContainerExecOpts{
		Args: []string{
			"go", "build",
			"-o", goSDKRuntimePath,
			".",
		},
		SkipEntrypoint: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to exec go build in go module sdk container runtime: %w", err)
	}

	ctr, err = ctr.UpdateImageConfig(ctx, func(cfg specs.ImageConfig) specs.ImageConfig {
		cfg.Entrypoint = []string{goSDKRuntimePath}
		return cfg
	})
	if err != nil {
		return nil, fmt.Errorf("failed to update image config for go module sdk container runtime: %w", err)
	}

	return ctr, nil
}

func (sdk *goSDK) baseWithCodegen(ctx context.Context, mod *UserMod) (*core.Container, error) {
	introspectionJSON, err := mod.DependencySchemaIntrospectionJSON(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get schema introspection json during %s module sdk codegen: %w", mod.Name(), err)
	}

	introspectionJSONFile, err := core.NewFileWithContents(ctx, sdk.bk, sdk.services,
		filepath.Base(goSDKIntrospectionJSONPath),
		[]byte(introspectionJSON), 0444, nil,
		nil, sdk.platform,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create introspection json file during go module sdk codegen: %w", err)
	}

	ctr, err := sdk.base(ctx)
	if err != nil {
		return nil, err
	}
	// delete dagger.gen.go if it exists, which is going to be overwritten anyways. If it doesn't exist, we ignore not found
	// in the implementation of `Without` so it will be a no-op
	sourceDir := mod.metadata.SourceDirectory
	sourceDir, err = sourceDir.Without(ctx, filepath.Join(mod.metadata.SourceDirectorySubpath, "dagger.gen.go"))
	if err != nil {
		return nil, fmt.Errorf("failed to remove dagger.gen.go from source directory: %w", err)
	}

	ctr, err = ctr.WithMountedFile(ctx, sdk.bk, goSDKIntrospectionJSONPath, introspectionJSONFile, "", true)
	if err != nil {
		return nil, fmt.Errorf("failed to mount introspection json file into go module sdk container codegen: %w", err)
	}
	ctr, err = ctr.WithMountedDirectory(ctx, sdk.bk, goSDKUserModSourceDirPath, sourceDir, "", false)
	if err != nil {
		return nil, fmt.Errorf("failed to mount module source into go module sdk container codegen: %w", err)
	}
	ctr, err = ctr.UpdateImageConfig(ctx, func(cfg specs.ImageConfig) specs.ImageConfig {
		cfg.WorkingDir = filepath.Join(goSDKUserModSourceDirPath, mod.metadata.SourceDirectorySubpath)
		cfg.Cmd = nil
		return cfg
	})
	if err != nil {
		return nil, fmt.Errorf("failed to update image config for go module sdk container codegen: %w", err)
	}

	ctr, err = ctr.WithExec(ctx, sdk.bk, sdk.progSockPath, sdk.platform, core.ContainerExecOpts{
		Args: []string{
			"--module", ".",
			"--propagate-logs=true",
			"--introspection-json-path", goSDKIntrospectionJSONPath,
		},
		ExperimentalPrivilegedNesting: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to exec go build in go module sdk container codegen: %w", err)
	}

	return ctr, nil
}

func (sdk *goSDK) base(ctx context.Context) (*core.Container, error) {
	ctx, recorder := progrock.WithGroup(ctx, "load builtin module sdk go")
	pbDef, err := sdk.bk.EngineContainerLocalImport(ctx, recorder, sdk.platform, filepath.Dir(distconsts.GoSDKEngineContainerTarballPath), nil, []string{filepath.Base(distconsts.GoSDKEngineContainerTarballPath)})
	if err != nil {
		return nil, fmt.Errorf("failed to import go module sdk tarball from engine container filesystem: %s", err)
	}
	tarballFile := core.NewFile(ctx, pbDef, filepath.Base(distconsts.GoSDKEngineContainerTarballPath), nil, sdk.platform, nil)
	tarballFileID, err := tarballFile.ID()
	if err != nil {
		return nil, fmt.Errorf("failed to get go module sdk tarball file id: %w", err)
	}

	ctr, err := core.NewContainer("", nil, sdk.platform)
	if err != nil {
		return nil, fmt.Errorf("failed to create new container for go module sdk: %w", err)
	}
	ctr, err = ctr.Import(
		ctx,
		tarballFileID,
		"",
		sdk.bk,
		sdk.host,
		sdk.services,
		sdk.importCache,
		sdk.ociStore,
		sdk.leaseManager,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to import go module sdk tarball: %w", err)
	}

	ctr, err = ctr.WithMountedCache(ctx, sdk.bk, "/go/pkg/mod", core.NewCache("modgomodcache"), nil, core.CacheSharingModeShared, "")
	if err != nil {
		return nil, fmt.Errorf("failed to mount go module cache into go module sdk container: %w", err)
	}
	ctr, err = ctr.WithMountedCache(ctx, sdk.bk, "/root/.cache/go-build", core.NewCache("modgobuildcache"), nil, core.CacheSharingModeShared, "")
	if err != nil {
		return nil, fmt.Errorf("failed to mount go build cache into go module sdk container: %w", err)
	}

	return ctr, nil
}
