package schema

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dagger/dagger/dagql"
	"github.com/opencontainers/go-digest"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/internal/distconsts"
)

const (
	runtimeWorkdirPath = "/scratch"
)

// load the SDK implementation with the given name for the module at the given source dir + subpath.
func (s *moduleSchema) sdkForModule(
	ctx context.Context,
	query *core.Query,
	sdk string,
	parentSrc dagql.Instance[*core.ModuleSource],
) (core.SDK, error) {
	if sdk == "" {
		return nil, errors.New("sdk ref is required")
	}

	builtinSDK, err := s.builtinSDK(ctx, query, sdk)
	if err == nil {
		return builtinSDK, nil
	} else if !errors.Is(err, errUnknownBuiltinSDK) {
		return nil, err
	}

	var sdkSource dagql.Instance[*core.ModuleSource]
	err = s.dag.Select(ctx, s.dag.Root(), &sdkSource,
		dagql.Selector{
			Field: "moduleSource",
			Args: []dagql.NamedInput{
				{Name: "refString", Value: dagql.String(sdk)},
			},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get sdk source for %s: %w", sdk, err)
	}

	if sdkSource.Self.Kind == core.ModuleSourceKindLocal {
		err = s.dag.Select(ctx, parentSrc, &sdkSource,
			dagql.Selector{
				Field: "resolveDependency",
				Args: []dagql.NamedInput{
					{Name: "dep", Value: dagql.NewID[*core.ModuleSource](sdkSource.ID())},
				},
			},
		)
		if err != nil {
			return nil, fmt.Errorf("failed to load sdk module %s: %w", sdk, err)
		}
	}

	var sdkMod dagql.Instance[*core.Module]
	err = s.dag.Select(ctx, sdkSource, &sdkMod,
		dagql.Selector{
			Field: "asModule",
		},
		dagql.Selector{
			Field: "initialize",
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load sdk module %s: %w", sdk, err)
	}

	// TODO: include sdk source dir from module config dagger.json once we support default-args/scripts
	return s.newModuleSDK(ctx, query, sdkMod, dagql.Instance[*core.Directory]{})
}

var errUnknownBuiltinSDK = fmt.Errorf("unknown builtin sdk")

// return a builtin SDK implementation with the given name
func (s *moduleSchema) builtinSDK(ctx context.Context, root *core.Query, sdkName string) (core.SDK, error) {
	switch sdkName {
	case "go":
		return &goSDK{root: root}, nil
	case "python":
		return s.loadBuiltinSDK(ctx, root, sdkName, digest.Digest(os.Getenv(distconsts.PythonSDKManifestDigestEnvName)))
	case "typescript":
		return s.loadBuiltinSDK(ctx, root, sdkName, digest.Digest(os.Getenv(distconsts.TypescriptSDKManifestDigestEnvName)))
	default:
		return nil, fmt.Errorf("%s: %w", sdkName, errUnknownBuiltinSDK)
	}
}

// moduleSDK is an SDK implemented as module; i.e. every module besides the special case go sdk.
type moduleSDK struct {
	// A query root that the SDK module has been installed to.
	root *core.Query
	// The SDK object retrieved from the server, for calling functions against.
	sdk dagql.Object
	// name of this module
	modName string
}

func (s *moduleSchema) newModuleSDK(
	ctx context.Context,
	root *core.Query,
	sdkModMeta dagql.Instance[*core.Module],
	optionalFullSDKSourceDir dagql.Instance[*core.Directory],
) (*moduleSDK, error) {
	newRoot, err := root.NewRootForDependencies(ctx, core.NewModDeps([]core.Mod{sdkModMeta.Self}))
	if err != nil {
		return nil, fmt.Errorf("failed to create new root for sdk module %s: %w", sdkModMeta.Self.Name(), err)
	}

	var sdk dagql.Object
	var constructorArgs []dagql.NamedInput
	if optionalFullSDKSourceDir.Self != nil {
		constructorArgs = []dagql.NamedInput{
			{Name: "sdkSourceDir", Value: dagql.Opt(dagql.NewID[*core.Directory](optionalFullSDKSourceDir.ID()))},
		}
	}
	if err := newRoot.Dag.Select(ctx, newRoot.Dag.Root(), &sdk,
		dagql.Selector{
			Field: gqlFieldName(sdkModMeta.Self.Name()),
			Args:  constructorArgs,
		},
	); err != nil {
		return nil, fmt.Errorf("failed to get sdk object for sdk module %s: %w", sdkModMeta.Self.Name(), err)
	}

	return &moduleSDK{root: newRoot, sdk: sdk, modName: sdkModMeta.Self.Name()}, nil
}

// Codegen calls the Codegen function on the SDK Module
func (sdk *moduleSDK) Codegen(ctx context.Context, modSrc dagql.Instance[*core.ModuleSource]) (*core.GeneratedCode, error) {
	var introspectionJSON core.JSON
	if err := sdk.root.Dag.Select(ctx, modSrc, &introspectionJSON,
		dagql.Selector{Field: "schemaIntrospectionJSON"},
	); err != nil {
		modName, _ := modSrc.Self.ModuleName(ctx)
		return nil, fmt.Errorf("failed to get schema introspection json for module %s during %s module sdk codegen: %w", modName, sdk.modName, err)
	}

	var inst dagql.Instance[*core.GeneratedCode]
	if err := sdk.root.Dag.Select(ctx, sdk.sdk, &inst, dagql.Selector{
		Field: "codegen",
		Args: []dagql.NamedInput{
			{
				Name:  "modSource",
				Value: dagql.NewID[*core.ModuleSource](modSrc.ID()),
			},
			{
				Name:  "introspectionJson",
				Value: dagql.String(introspectionJSON),
			},
		},
	}); err != nil {
		return nil, fmt.Errorf("failed to call sdk module codegen: %w", err)
	}
	return inst.Self, nil
}

// Runtime calls the Runtime function on the SDK Module
func (sdk *moduleSDK) Runtime(ctx context.Context, modSrc dagql.Instance[*core.ModuleSource]) (*core.Container, error) {
	var introspectionJSON core.JSON
	if err := sdk.root.Dag.Select(ctx, modSrc, &introspectionJSON,
		dagql.Selector{Field: "schemaIntrospectionJSON"},
	); err != nil {
		modName, _ := modSrc.Self.ModuleName(ctx)
		return nil, fmt.Errorf("failed to get schema introspection json for module %s during %s module sdk codegen: %w", modName, sdk.modName, err)
	}

	var inst dagql.Instance[*core.Container]
	if err := sdk.root.Dag.Select(ctx, sdk.sdk, &inst,
		dagql.Selector{
			Field: "moduleRuntime",
			Args: []dagql.NamedInput{
				{
					Name:  "modSource",
					Value: dagql.NewID[*core.ModuleSource](modSrc.ID()),
				},
				{
					Name:  "introspectionJson",
					Value: dagql.String(introspectionJSON),
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
	); err != nil {
		return nil, fmt.Errorf("failed to call sdk module moduleRuntime: %w", err)
	}
	return inst.Self, nil
}

func (sdk *moduleSDK) RequiredPaths(ctx context.Context) ([]string, error) {
	var paths []string
	err := sdk.root.Dag.Select(ctx, sdk.sdk, &paths,
		dagql.Selector{
			Field: "requiredPaths",
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to call sdk module requiredPaths: %w", err)
	}
	return paths, nil
}

// loadBuiltinSDK loads an SDK implemented as a module that is "builtin" to engine, which means its pre-packaged
// with the engine container in order to enable use w/out hard dependencies on the internet
func (s *moduleSchema) loadBuiltinSDK(
	ctx context.Context,
	root *core.Query,
	name string,
	manifestDigest digest.Digest,
) (*moduleSDK, error) {
	// TODO: currently hardcoding assumption that builtin sdks put *module* source code at
	// "runtime" subdir right under the *full* sdk source dir. Can be generalized once we support
	// default-args/scripts in dagger.json
	var fullSDKDir dagql.Instance[*core.Directory]
	if err := s.dag.Select(ctx, s.dag.Root(), &fullSDKDir,
		dagql.Selector{
			Field: "builtinContainer",
			Args: []dagql.NamedInput{
				{
					Name:  "digest",
					Value: dagql.String(manifestDigest.String()),
				},
			},
		},
		dagql.Selector{
			Field: "rootfs",
		},
	); err != nil {
		return nil, fmt.Errorf("failed to import full sdk source for sdk %s from engine container filesystem: %w", name, err)
	}

	var sdkModDir dagql.Instance[*core.Directory]
	err := s.dag.Select(ctx, fullSDKDir, &sdkModDir,
		dagql.Selector{
			Field: "directory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String("runtime")},
			},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to import module sdk %s: %w", name, err)
	}

	var sdkMod dagql.Instance[*core.Module]
	err = s.dag.Select(ctx, sdkModDir, &sdkMod,
		dagql.Selector{
			Field: "asModule",
		},
		dagql.Selector{
			Field: "initialize",
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load embedded sdk module %q: %w", name, err)
	}

	return s.newModuleSDK(ctx, root, sdkMod, fullSDKDir)
}

const (
	goSDKUserModContextDirPath = "/src"
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
}

func (sdk *goSDK) Codegen(ctx context.Context, modSrc dagql.Instance[*core.ModuleSource]) (*core.GeneratedCode, error) {
	ctr, err := sdk.baseWithCodegen(ctx, modSrc)
	if err != nil {
		return nil, err
	}

	var modifiedSrcDir dagql.Instance[*core.Directory]
	if err := sdk.root.Dag.Select(ctx, ctr, &modifiedSrcDir, dagql.Selector{
		Field: "directory",
		Args: []dagql.NamedInput{
			{
				Name:  "path",
				Value: dagql.String(goSDKUserModContextDirPath),
			},
		},
	}); err != nil {
		return nil, fmt.Errorf("failed to get modified source directory for go module sdk codegen: %w", err)
	}

	return &core.GeneratedCode{
		Code: modifiedSrcDir,
		VCSGeneratedPaths: []string{
			"dagger.gen.go",
			"internal/dagger/**",
			"internal/querybuilder/**",
		},
		VCSIgnoredPaths: []string{
			"dagger.gen.go",
			"internal/dagger",
			"internal/querybuilder",
		},
	}, nil
}

func (sdk *goSDK) Runtime(ctx context.Context, modSrc dagql.Instance[*core.ModuleSource]) (*core.Container, error) {
	ctr, err := sdk.baseWithCodegen(ctx, modSrc)
	if err != nil {
		return nil, err
	}
	if err := sdk.root.Dag.Select(ctx, ctr, &ctr,
		dagql.Selector{
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
		},
		dagql.Selector{
			Field: "withEntrypoint",
			Args: []dagql.NamedInput{
				{
					Name: "args",
					Value: dagql.ArrayInput[dagql.String]{
						goSDKRuntimePath,
					},
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
	); err != nil {
		return nil, fmt.Errorf("failed to exec go build in go module sdk container runtime: %w", err)
	}
	return ctr.Self, nil
}

func (sdk *goSDK) RequiredPaths(_ context.Context) ([]string, error) {
	return []string{
		"**/go.mod",
		"**/go.sum",
		"**/go.work",
		"**/go.work.sum",
		// TODO: the below could be optimized by scoping only to go modules that actually
		// end up being needed for the dagger module.
		// including vendor/ is potentially expensive, but required
		"**/vendor/",
		// needed in order to re-use go.mod from any parent dir (otherwise it's an invalid go module)
		"**/*.go",
	}, nil
}

func (sdk *goSDK) baseWithCodegen(ctx context.Context, modSrc dagql.Instance[*core.ModuleSource]) (dagql.Instance[*core.Container], error) {
	var ctr dagql.Instance[*core.Container]

	var introspectionJSON core.JSON
	if err := sdk.root.Dag.Select(ctx, modSrc, &introspectionJSON,
		dagql.Selector{Field: "schemaIntrospectionJSON"},
	); err != nil {
		modName, _ := modSrc.Self.ModuleName(ctx)
		return ctr, fmt.Errorf("failed to get schema introspection json for module %s during go sdk codegen: %w",
			modName, err,
		)
	}

	modName, err := modSrc.Self.ModuleOriginalName(ctx)
	if err != nil {
		return ctr, fmt.Errorf("failed to get module name for go module sdk codegen: %w", err)
	}

	contextDir, err := modSrc.Self.ContextDirectory()
	if err != nil {
		return ctr, fmt.Errorf("failed to get context directory for go module sdk codegen: %w", err)
	}
	srcSubpath, err := modSrc.Self.SourceSubpathWithDefault(ctx)
	if err != nil {
		return ctr, fmt.Errorf("failed to get subpath for go module sdk codegen: %w", err)
	}

	ctr, err = sdk.base(ctx)
	if err != nil {
		return ctr, err
	}

	// Make the source subpath if it doesn't exist already.
	// Also rm dagger.gen.go if it exists, which is going to be overwritten
	// anyways. If it doesn't exist, we ignore not found in the implementation of
	// `withoutFile` so it will be a no-op.
	var emptyDir dagql.Instance[*core.Directory]
	if err := sdk.root.Dag.Select(ctx, sdk.root.Dag.Root(), &emptyDir, dagql.Selector{Field: "directory"}); err != nil {
		return ctr, fmt.Errorf("failed to create empty directory for go module sdk codegen: %w", err)
	}

	var updatedContextDir dagql.Instance[*core.Directory]
	if err := sdk.root.Dag.Select(ctx, contextDir, &updatedContextDir,
		dagql.Selector{
			Field: "withDirectory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(srcSubpath)},
				{Name: "directory", Value: dagql.NewID[*core.Directory](emptyDir.ID())},
			},
		},
		dagql.Selector{
			Field: "withoutFile",
			Args: []dagql.NamedInput{
				{
					Name:  "path",
					Value: dagql.String(filepath.Join(srcSubpath, "dagger.gen.go")),
				},
			},
		},
	); err != nil {
		return ctr, fmt.Errorf("failed to remove dagger.gen.go from source directory: %w", err)
	}

	if err := sdk.root.Dag.Select(ctx, ctr, &ctr, dagql.Selector{
		Field: "withNewFile",
		Args: []dagql.NamedInput{
			{
				Name:  "path",
				Value: dagql.NewString(goSDKIntrospectionJSONPath),
			},
			{
				Name:  "contents",
				Value: dagql.String(introspectionJSON),
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
				Value: dagql.NewString(goSDKUserModContextDirPath),
			},
			{
				Name:  "source",
				Value: dagql.NewID[*core.Directory](updatedContextDir.ID()),
			},
		},
	}, dagql.Selector{
		Field: "withWorkdir",
		Args: []dagql.NamedInput{
			{
				Name:  "path",
				Value: dagql.NewString(filepath.Join(goSDKUserModContextDirPath, srcSubpath)),
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
					"--module-context", goSDKUserModContextDirPath,
					"--module-name", dagql.String(modName),
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

	var modCache dagql.Instance[*core.CacheVolume]
	if err := sdk.root.Dag.Select(ctx, sdk.root.Dag.Root(), &modCache, dagql.Selector{
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
	if err := sdk.root.Dag.Select(ctx, sdk.root.Dag.Root(), &buildCache, dagql.Selector{
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
	if err := sdk.root.Dag.Select(ctx, sdk.root.Dag.Root(), &ctr, dagql.Selector{
		Field: "builtinContainer",
		Args: []dagql.NamedInput{
			{
				Name:  "digest",
				Value: dagql.String(os.Getenv(distconsts.GoSDKManifestDigestEnvName)),
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
