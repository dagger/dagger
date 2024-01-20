package schema

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/dagger/dagger/dagql"
	"github.com/vito/progrock"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/internal/distconsts"
)

// load the SDK implementation with the given name for the module at the given source dir + subpath.
func (s *moduleSchema) sdkForModule(
	ctx context.Context,
	root *core.Query,
	sdk string,
	parentDir dagql.Instance[*core.Directory],
) (core.SDK, error) {
	builtinSDK, err := s.builtinSDK(ctx, root, sdk)
	if err == nil {
		return builtinSDK, nil
	} else if !errors.Is(err, errUnknownBuiltinSDK) {
		return nil, err
	}

	var sdkMod dagql.Instance[*core.Module]
	err = s.dag.Select(ctx, s.dag.Root(), &sdkMod,
		dagql.Selector{
			Field: "moduleRef",
			Args: []dagql.NamedInput{
				{Name: "refString", Value: dagql.String(sdk)},
				{Name: "parentDirectory", Value: dagql.NewID[*core.Directory](parentDir.ID())},
			},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load sdk module %s: %w", sdk, err)
	}

	return s.newModuleSDK(ctx, root, sdkMod)
}

var errUnknownBuiltinSDK = fmt.Errorf("unknown builtin sdk")

// return a builtin SDK implementation with the given name
func (s *moduleSchema) builtinSDK(ctx context.Context, root *core.Query, sdkName string) (core.SDK, error) {
	switch sdkName {
	case "go":
		return &goSDK{root: root, dag: s.dag}, nil
	case "python":
		return s.loadBuiltinSDK(ctx, root, sdkName, distconsts.PythonSDKEngineContainerModulePath)
	case "typescript":
		return s.loadBuiltinSDK(ctx, root, sdkName, distconsts.TypescriptSDKEngineContainerModulePath)
	default:
		return nil, fmt.Errorf("%s: %w", sdkName, errUnknownBuiltinSDK)
	}
}

// moduleSDK is an SDK implemented as module; i.e. every module besides the special case go sdk.
type moduleSDK struct {
	// The module implementing this SDK.
	mod dagql.Instance[*core.Module]
	// A server that the SDK module has been installed to.
	dag *dagql.Server
	// The SDK object retrieved from the server, for calling functions against.
	sdk dagql.Object
}

func (s *moduleSchema) newModuleSDK(ctx context.Context, root *core.Query, sdkModMeta dagql.Instance[*core.Module]) (*moduleSDK, error) {
	dag := dagql.NewServer[*core.Query](root)
	dag.Cache = root.Cache
	if err := sdkModMeta.Self.Install(ctx, dag); err != nil {
		return nil, fmt.Errorf("failed to install sdk module %s: %w", sdkModMeta.Self.Name(), err)
	}
	var sdk dagql.Object
	if err := dag.Select(ctx, dag.Root(), &sdk, dagql.Selector{
		Field: gqlFieldName(sdkModMeta.Self.Name()),
	}); err != nil {
		return nil, fmt.Errorf("failed to get sdk object for sdk module %s: %w", sdkModMeta.Self.Name(), err)
	}
	return &moduleSDK{mod: sdkModMeta, dag: dag, sdk: sdk}, nil
}

// Codegen calls the Codegen function on the SDK Module
//
//nolint:dupl
func (sdk *moduleSDK) Codegen(ctx context.Context, mod *core.Module, source dagql.Instance[*core.ModuleSource]) (*core.GeneratedCode, error) {
	introspectionJSON, err := mod.DependencySchemaIntrospectionJSON(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get schema introspection json during %s module sdk codegen: %w", sdk.mod.Self.Name(), err)
	}

	var inst dagql.Instance[*core.GeneratedCode]
	err = sdk.dag.Select(ctx, sdk.sdk, &inst, dagql.Selector{
		Field: "codegen",
		Args: []dagql.NamedInput{
			{
				Name:  "modSource",
				Value: dagql.NewID[*core.ModuleSource](source.ID()),
			},
			{
				Name:  "introspectionJson",
				Value: dagql.String(introspectionJSON),
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to call sdk module codegen: %w", err)
	}
	return inst.Self, nil
}

// Runtime calls the Runtime function on the SDK Module
//
//nolint:dupl
func (sdk *moduleSDK) Runtime(ctx context.Context, mod *core.Module, source dagql.Instance[*core.ModuleSource]) (*core.Container, error) {
	introspectionJSON, err := mod.DependencySchemaIntrospectionJSON(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get schema introspection json during %s module sdk runtime: %w", sdk.mod.Self.Name(), err)
	}

	var inst dagql.Instance[*core.Container]
	err = sdk.dag.Select(ctx, sdk.sdk, &inst, dagql.Selector{
		Field: "moduleRuntime",
		Args: []dagql.NamedInput{
			{
				Name:  "modSource",
				Value: dagql.NewID[*core.ModuleSource](source.ID()),
			},
			{
				Name:  "introspectionJson",
				Value: dagql.String(introspectionJSON),
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to call sdk module moduleRuntime: %w", err)
	}
	return inst.Self, nil
}

// loadBuiltinSDK loads an SDK implemented as a module that is "builtin" to engine, which means its pre-packaged
// with the engine container in order to enable use w/out hard dependencies on the internet
func (s *moduleSchema) loadBuiltinSDK(
	ctx context.Context,
	root *core.Query,
	name string,
	engineContainerModulePath string,
) (*moduleSDK, error) {
	ctx, recorder := progrock.WithGroup(ctx, fmt.Sprintf("load builtin module sdk %s", name))

	// TODO: currently hardcoding assumption that builtin sdks put module source code at
	// subdir right under their config dir. Include/exlude won't be used to optimize loading
	// yet. Can be lifted with some effort but currently works for the existing builtin sdks.
	configDirPath := filepath.Dir(engineContainerModulePath)
	sourceRelPath, err := filepath.Rel(configDirPath, engineContainerModulePath)
	if err != nil {
		return nil, fmt.Errorf("failed to get relative path of module sdk %s: %w", name, err)
	}

	_, desc, err := root.Buildkit.EngineContainerLocalImport(
		ctx,
		recorder,
		root.Platform.Spec(),
		configDirPath,
		nil,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to import module sdk %s from engine container filesystem: %w", name, err)
	}

	sdkDir, err := core.LoadBlob(ctx, s.dag, desc)
	if err != nil {
		return nil, fmt.Errorf("failed to load module sdk %s: %w", name, err)
	}

	var sdkMod dagql.Instance[*core.Module]
	err = s.dag.Select(ctx, sdkDir, &sdkMod,
		dagql.Selector{
			Field: "asModule",
			Args: []dagql.NamedInput{
				{
					Name:  "sourceSubpath",
					Value: dagql.String(sourceRelPath),
				},
			},
		},
		dagql.Selector{
			Field: "initialize",
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load embedded sdk module %q: %w", name, err)
	}

	return s.newModuleSDK(ctx, root, sdkMod)
}

const (
	goSDKUserModSourceDirPath  = "/src"
	goSDKRuntimePath           = "/runtime"
	goSDKIntrospectionJSONPath = "/schema.json"
)

/*
goSDK is the one special sdk not implemented as module, instead the
`cmd/codegen/` binary is packaged into a container w/ the go runtime,
tarball'd up and included in the engine image.

The Codegen and Runtime methods are implemented by loading that tarball and
executing the codegen binary inside it to generate user code and then execute
it with the resulting /runtime binary.
*/
type goSDK struct {
	root *core.Query
	dag  *dagql.Server
}

func (sdk *goSDK) Codegen(
	ctx context.Context,
	mod *core.Module,
	source dagql.Instance[*core.ModuleSource],
) (*core.GeneratedCode, error) {
	configDir := source.Self.RootDirectory
	sourceDirSubpath, err := source.Self.Subpath()
	if err != nil {
		return nil, fmt.Errorf("failed to get source subpath for go module sdk codegen: %w", err)
	}

	ctr, err := sdk.baseWithCodegen(ctx, mod, configDir, sourceDirSubpath)
	if err != nil {
		return nil, err
	}

	var modifiedSrcDir dagql.Instance[*core.Directory]
	if err := sdk.dag.Select(ctx, ctr, &modifiedSrcDir, dagql.Selector{
		Field: "directory",
		Args: []dagql.NamedInput{
			{
				Name:  "path",
				Value: dagql.String(goSDKUserModSourceDirPath),
			},
		},
	}); err != nil {
		return nil, fmt.Errorf("failed to get modified source directory for go module sdk codegen: %w", err)
	}

	var generated dagql.Instance[*core.Directory]
	if err := sdk.dag.Select(ctx, configDir, &generated, dagql.Selector{
		Field: "diff",
		Args: []dagql.NamedInput{
			{
				Name:  "other",
				Value: dagql.NewID[*core.Directory](modifiedSrcDir.ID()),
			},
		},
	}); err != nil {
		return nil, fmt.Errorf("failed to get generated source directory for go module sdk codegen: %w", err)
	}

	return &core.GeneratedCode{
		Code: generated,
		VCSGeneratedPaths: []string{
			"dagger.gen.go",
			"querybuilder/**",
		},
	}, nil
}

func (sdk *goSDK) Runtime(
	ctx context.Context,
	mod *core.Module,
	source dagql.Instance[*core.ModuleSource],
) (*core.Container, error) {
	configDir := source.Self.RootDirectory
	sourceDirSubpath, err := source.Self.Subpath()
	if err != nil {
		return nil, fmt.Errorf("failed to get source subpath for go module sdk codegen: %w", err)
	}

	ctr, err := sdk.baseWithCodegen(ctx, mod, configDir, sourceDirSubpath)
	if err != nil {
		return nil, err
	}
	if err := sdk.dag.Select(ctx, ctr, &ctr, dagql.Selector{
		Field: "withExec",
		Args: []dagql.NamedInput{
			{
				Name: "args",
				Value: dagql.ArrayInput[dagql.String]{
					"go", "build",
					"-o", goSDKRuntimePath,
					".",
				},
			},
			{
				Name:  "skipEntrypoint",
				Value: dagql.NewBoolean(true),
			},
		},
	}, dagql.Selector{
		Field: "withEntrypoint",
		Args: []dagql.NamedInput{
			{
				Name: "args",
				Value: dagql.ArrayInput[dagql.String]{
					goSDKRuntimePath,
				},
			},
		},
	}); err != nil {
		return nil, fmt.Errorf("failed to exec go build in go module sdk container runtime: %w", err)
	}
	return ctr.Self, nil
}

func (sdk *goSDK) baseWithCodegen(
	ctx context.Context,
	mod *core.Module,
	configDir dagql.Instance[*core.Directory],
	sourceDirSubpath string,
) (dagql.Instance[*core.Container], error) {
	var ctr dagql.Instance[*core.Container]

	introspectionJSON, err := mod.DependencySchemaIntrospectionJSON(ctx)
	if err != nil {
		return ctr, fmt.Errorf("failed to get schema introspection json during %s module sdk codegen: %w", mod.Name(), err)
	}

	ctr, err = sdk.base(ctx)
	if err != nil {
		return ctr, err
	}

	// Delete dagger.gen.go if it exists, which is going to be overwritten
	// anyways. If it doesn't exist, we ignore not found in the implementation of
	// `withoutFile` so it will be a no-op.

	// need to put this in a separate instance, otherwise the ID won't be updated when used as an arg below
	var updatedConfigDir dagql.Instance[*core.Directory]
	if err := sdk.dag.Select(ctx, configDir, &updatedConfigDir, dagql.Selector{
		Field: "withoutFile",
		Args: []dagql.NamedInput{
			{
				Name:  "path",
				Value: dagql.String(filepath.Join(sourceDirSubpath, "dagger.gen.go")),
			},
		},
	}); err != nil {
		return ctr, fmt.Errorf("failed to remove dagger.gen.go from source directory: %w", err)
	}

	if err := sdk.dag.Select(ctx, ctr, &ctr, dagql.Selector{
		Field: "withNewFile",
		Args: []dagql.NamedInput{
			{
				Name:  "path",
				Value: dagql.NewString(goSDKIntrospectionJSONPath),
			},
			{
				Name:  "contents",
				Value: dagql.NewString(introspectionJSON),
			},
			{
				Name:  "permissions",
				Value: dagql.NewInt(0444),
			},
		},
	}, dagql.Selector{
		Field: "withMountedDirectory",
		Args: []dagql.NamedInput{
			{
				Name:  "path",
				Value: dagql.NewString(goSDKUserModSourceDirPath),
			},
			{
				Name:  "source",
				Value: dagql.NewID[*core.Directory](updatedConfigDir.ID()),
			},
		},
	}, dagql.Selector{
		Field: "withWorkdir",
		Args: []dagql.NamedInput{
			{
				Name:  "path",
				Value: dagql.NewString(filepath.Join(goSDKUserModSourceDirPath, sourceDirSubpath)),
			},
		},
	}, dagql.Selector{
		Field: "withoutDefaultArgs",
	}, dagql.Selector{
		Field: "withExec",
		Args: []dagql.NamedInput{
			{
				Name: "args",
				Value: dagql.ArrayInput[dagql.String]{
					"--module-source-root", goSDKUserModSourceDirPath,
					"--module-name", dagql.String(mod.NameField),
					"--propagate-logs=true",
					"--introspection-json-path", goSDKIntrospectionJSONPath,
				},
			},
			{
				Name:  "experimentalPrivilegedNesting",
				Value: dagql.NewBoolean(true),
			},
		},
	}); err != nil {
		return ctr, fmt.Errorf("failed to mount introspection json file into go module sdk container codegen: %w", err)
	}

	return ctr, nil
}

func (sdk *goSDK) base(ctx context.Context) (dagql.Instance[*core.Container], error) {
	var inst dagql.Instance[*core.Container]
	ctx, recorder := progrock.WithGroup(ctx, "load builtin module sdk go")
	tarDir, tarName := filepath.Split(distconsts.GoSDKEngineContainerTarballPath)
	_, desc, err := sdk.root.Buildkit.EngineContainerLocalImport(
		ctx,
		recorder,
		sdk.root.Platform.Spec(),
		tarDir,
		nil,
		[]string{tarName},
	)
	if err != nil {
		return inst, fmt.Errorf("failed to import go module sdk tarball from engine container filesystem: %s", err)
	}
	blobDir, err := core.LoadBlob(ctx, sdk.dag, desc)
	if err != nil {
		return inst, fmt.Errorf("failed to load go module sdk tarball: %w", err)
	}
	var tarballFile dagql.Instance[*core.File]
	if err := sdk.dag.Select(ctx, blobDir, &tarballFile, dagql.Selector{
		Field: "file",
		Args: []dagql.NamedInput{
			{
				Name:  "path",
				Value: dagql.String(tarName),
			},
		},
	}); err != nil {
		return inst, fmt.Errorf("failed to get tarball file from go module sdk tarball: %w", err)
	}
	var modCache dagql.Instance[*core.CacheVolume]
	if err := sdk.dag.Select(ctx, sdk.dag.Root(), &modCache, dagql.Selector{
		Field: "cacheVolume",
		Args: []dagql.NamedInput{
			{
				Name:  "key",
				Value: dagql.String("modgomodcache"),
			},
		},
	}); err != nil {
		return inst, fmt.Errorf("failed to get mod cache from go module sdk tarball: %w", err)
	}
	var buildCache dagql.Instance[*core.CacheVolume]
	if err := sdk.dag.Select(ctx, sdk.dag.Root(), &buildCache, dagql.Selector{
		Field: "cacheVolume",
		Args: []dagql.NamedInput{
			{
				Name:  "key",
				Value: dagql.String("modgobuildcache"),
			},
		},
	}); err != nil {
		return inst, fmt.Errorf("failed to get build cache from go module sdk tarball: %w", err)
	}
	var ctr dagql.Instance[*core.Container]
	if err := sdk.dag.Select(ctx, sdk.dag.Root(), &ctr, dagql.Selector{
		Field: "container",
	}, dagql.Selector{
		Field: "import",
		Args: []dagql.NamedInput{
			{
				Name:  "source",
				Value: dagql.NewID[*core.File](tarballFile.ID()),
			},
		},
	}, dagql.Selector{
		Field: "withMountedCache",
		Args: []dagql.NamedInput{
			{
				Name:  "path",
				Value: dagql.String("/go/pkg/mod"),
			},
			{
				Name:  "cache",
				Value: dagql.NewID[*core.CacheVolume](modCache.ID()),
			},
			{
				Name:  "sharing",
				Value: core.CacheSharingModeShared,
			},
		},
	}, dagql.Selector{
		Field: "withMountedCache",
		Args: []dagql.NamedInput{
			{
				Name:  "path",
				Value: dagql.String("/root/.cache/go-build"),
			},
			{
				Name:  "cache",
				Value: dagql.NewID[*core.CacheVolume](buildCache.ID()),
			},
			{
				Name:  "sharing",
				Value: core.CacheSharingModeShared,
			},
		},
	}); err != nil {
		return inst, fmt.Errorf("failed to get container from go module sdk tarball: %w", err)
	}
	return ctr, nil
}
