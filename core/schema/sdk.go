package schema

import (
	"errors"
	"fmt"
	"path/filepath"

	"dagger.io/dagger/modules"
	"github.com/dagger/dagger/core"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/vito/progrock"
)

// TODO: doc
type SDK interface {
	Codegen(ctx *core.Context, sourceDir *core.Directory, sourceDirSubpath string) (*core.GeneratedCode, error)
	Runtime(ctx *core.Context, sourceDir *core.Directory, sourceDirSubpath string) (*core.Container, error)
}

// TODO: doc assumptions about state of mod initialization, or go back to just accepting sourcedir/subpath here?
func (s *moduleSchema) runtimeForModule(ctx *core.Context, mod *core.Module) (*core.Container, error) {
	sdk, err := s.sdkForModule(ctx, mod)
	if err != nil {
		return nil, fmt.Errorf("failed to get sdk %q for module: %w", mod.Config.SDK, err)
	}
	return sdk.Runtime(ctx, mod.SourceDirectory, mod.SourceDirectorySubpath)
}

// TODO: doc assumptions about state of mod initialization
func (s *moduleSchema) sdkForModule(ctx *core.Context, mod *core.Module) (SDK, error) {
	builtinSDK, err := s.builtinSDK(ctx, mod.Config.SDK)
	if err == nil {
		return builtinSDK, nil
	} else if !errors.Is(err, errUnknownBuiltinSDK) {
		return nil, err
	}

	return s.newModuleSDK(ctx, mod.SourceDirectory, mod.Config.SDK)
}

var errUnknownBuiltinSDK = fmt.Errorf("unknown builtin sdk")

func (s *moduleSchema) builtinSDK(ctx *core.Context, sdkName string) (SDK, error) {
	switch sdkName {
	case "go":
		return &goSDK{moduleSchema: s}, nil
	case "python":
		return s.loadBuiltinSDK(ctx, &builtinSDKParams{
			name: sdkName,
			// TODO: don't hardcode paths here, dedupe w/ CI
			engineContainerModulePath: "/usr/local/share/dagger/python-sdk/runtime",
			// TODO: excludePaths, or just read from dagger.json?
			// TODO: includePaths, or just read from dagger.json?
		})
	default:
		return nil, fmt.Errorf("%s: %w", sdkName, errUnknownBuiltinSDK)
	}
}

type moduleSDK struct {
	*moduleSchema
	// The module defining this SDK.
	mod *core.Module
}

func (s *moduleSchema) newModuleSDK(ctx *core.Context, sourceDir *core.Directory, configPath string) (*moduleSDK, error) {
	mod, err := core.NewModule(s.platform, nil).FromConfig(
		ctx,
		s.bk,
		s.services,
		s.progSockPath,
		sourceDir,
		configPath,
		s.runtimeForModule,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load sdk module: %w", err)
	}
	mod, err = s.loadModuleTypes(ctx, mod)
	if err != nil {
		return nil, fmt.Errorf("failed to load sdk module types: %w", err)
	}

	return &moduleSDK{moduleSchema: s, mod: mod}, nil
}

func (sdk *moduleSDK) Codegen(ctx *core.Context, sourceDir *core.Directory, sourceDirSubpath string) (*core.GeneratedCode, error) {
	moduleName := gqlObjectName(sdk.mod.Name)
	funcName := "Codegen"
	var codegenFn *core.Function
	for _, obj := range sdk.mod.Objects {
		if obj.AsObject.Name == moduleName {
			for _, fn := range obj.AsObject.Functions {
				if fn.Name == funcName {
					codegenFn = fn
					break
				}
			}
		}
	}
	if codegenFn == nil {
		return nil, fmt.Errorf("failed to find required Codegen function in SDK module %s", moduleName)
	}

	srcDirID, err := sourceDir.ID()
	if err != nil {
		return nil, fmt.Errorf("failed to get source directory id: %w", err)
	}

	result, err := sdk.moduleSchema.functionCall(ctx, codegenFn, functionCallArgs{
		Module: sdk.mod,
		Input: []*core.CallInput{{
			Name:  "modSource",
			Value: srcDirID,
		}, {
			Name:  "subPath",
			Value: sourceDirSubpath,
		}},
		ParentName: moduleName,
		// TODO: params? somehow? maybe from module config? would be a good way to
		// e.g. configure the language version.
		Parent: map[string]any{},
		Cache:  true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to call sdk module: %w", err)
	}

	genCodeID, ok := result.(string)
	if !ok {
		return nil, fmt.Errorf("expected string directory ID result, got %T", result)
	}

	return core.GeneratedCodeID(genCodeID).Decode()
}

func (sdk *moduleSDK) Runtime(ctx *core.Context, sourceDir *core.Directory, sourceDirSubpath string) (*core.Container, error) {
	moduleName := gqlObjectName(sdk.mod.Name)
	funcName := "ModuleRuntime"
	var getRuntimeFn *core.Function
	for _, obj := range sdk.mod.Objects {
		if obj.AsObject.Name == moduleName {
			for _, fn := range obj.AsObject.Functions {
				if fn.Name == funcName {
					getRuntimeFn = fn
					break
				}
			}
		}
	}
	if getRuntimeFn == nil {
		return nil, fmt.Errorf("failed to find required ModuleRuntime function in SDK module %s", moduleName)
	}

	srcDirID, err := sourceDir.ID()
	if err != nil {
		return nil, fmt.Errorf("failed to get source directory id: %w", err)
	}

	result, err := sdk.moduleSchema.functionCall(ctx, getRuntimeFn, functionCallArgs{
		Module: sdk.mod,
		Input: []*core.CallInput{{
			Name:  "modSource",
			Value: srcDirID,
		}, {
			Name:  "subPath",
			Value: sourceDirSubpath,
		}},
		ParentName: moduleName,
		// TODO: params? somehow? maybe from module config? would be a good way to
		// e.g. configure the language version.
		Parent: map[string]any{},
		Cache:  true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to call sdk module: %w", err)
	}

	runtimeID, ok := result.(string)
	if !ok {
		return nil, fmt.Errorf("expected string container ID result, got %T", result)
	}

	runtime, err := core.ContainerID(runtimeID).Decode()
	if err != nil {
		return nil, fmt.Errorf("failed to decode container: %w", err)
	}
	return runtime, nil
}

type builtinSDKParams struct {
	name                      string
	engineContainerModulePath string
	excludePaths              []string
	includePaths              []string
}

func (s *moduleSchema) loadBuiltinSDK(ctx *core.Context, params *builtinSDKParams) (*moduleSDK, error) {
	progCtx, recorder := progrock.WithGroup(ctx.Context, fmt.Sprintf("load builtin module sdk %s", params.name))
	ctx.Context = progCtx

	cfgPath := modules.NormalizeConfigPath(params.engineContainerModulePath)
	cfgPBDef, err := s.bk.EngineContainerLocalImport(
		ctx,
		recorder,
		s.platform,
		filepath.Dir(cfgPath),
		nil,
		[]string{filepath.Base(cfgPath)},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to import module sdk config file %s from engine container filesystem: %s", params.name, err)
	}

	cfgFile := core.NewFile(ctx, cfgPBDef, filepath.Base(cfgPath), nil, s.platform, nil)
	modCfg, err := core.LoadModuleConfig(ctx, s.bk, s.services, cfgFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load module sdk config file %s: %w", params.name, err)
	}

	modRootPath := filepath.Join(filepath.Dir(cfgPath), modCfg.Root)
	pbDef, err := s.bk.EngineContainerLocalImport(
		ctx,
		recorder,
		s.platform,
		modRootPath,
		params.excludePaths,
		params.includePaths,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to import module sdk %s from engine container filesystem: %s", params.name, err)
	}

	cfgRelPath, err := filepath.Rel(modRootPath, cfgPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get relative path of module sdk config file %s: %w", params.name, err)
	}

	return s.newModuleSDK(ctx, core.NewDirectory(ctx, pbDef, "/", nil, s.platform, nil), cfgRelPath)
}

type goSDK struct {
	*moduleSchema
}

func (sdk *goSDK) Codegen(ctx *core.Context, sourceDir *core.Directory, sourceDirSubpath string) (*core.GeneratedCode, error) {
	ctr, err := sdk.baseWithCodegen(ctx, sourceDir, sourceDirSubpath)
	if err != nil {
		return nil, err
	}

	modifiedSrcDir, err := ctr.Directory(ctx, sdk.bk, sdk.services, core.ModSourceDirPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get modified source directory for go module sdk codegen: %w", err)
	}

	diff, err := sourceDir.Diff(ctx, modifiedSrcDir)
	if err != nil {
		return nil, fmt.Errorf("failed to get diff: %w", err)
	}
	// diff needs to be of the subdir, if any, not necessarily the root
	diff, err = diff.Directory(ctx, sdk.bk, sdk.services, sourceDirSubpath)
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

func (sdk *goSDK) Runtime(ctx *core.Context, sourceDir *core.Directory, sourceDirSubpath string) (*core.Container, error) {
	ctr, err := sdk.baseWithCodegen(ctx, sourceDir, sourceDirSubpath)
	if err != nil {
		return nil, err
	}

	ctr, err = ctr.WithExec(ctx, sdk.bk, sdk.progSockPath, sdk.platform, core.ContainerExecOpts{
		Args: []string{
			"go", "build",
			"-o", core.RuntimeExecutablePath,
			".",
		},
		SkipEntrypoint:                true,
		ExperimentalPrivilegedNesting: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to exec go build in go module sdk container runtime: %w", err)
	}

	ctr, err = ctr.UpdateImageConfig(ctx, func(cfg specs.ImageConfig) specs.ImageConfig {
		cfg.Entrypoint = []string{core.RuntimeExecutablePath}
		return cfg
	})
	if err != nil {
		return nil, fmt.Errorf("failed to update image config for go module sdk container runtime: %w", err)
	}

	return ctr, nil
}

func (sdk *goSDK) baseWithCodegen(ctx *core.Context, sourceDir *core.Directory, sourceDirSubpath string) (*core.Container, error) {
	ctr, err := sdk.base(ctx)
	if err != nil {
		return nil, err
	}

	ctr, err = ctr.WithMountedDirectory(ctx, sdk.bk, core.ModSourceDirPath, sourceDir, "", false)
	if err != nil {
		return nil, fmt.Errorf("failed to mount module source into go module sdk container codegen: %w", err)
	}
	ctr, err = ctr.UpdateImageConfig(ctx, func(cfg specs.ImageConfig) specs.ImageConfig {
		cfg.WorkingDir = filepath.Join(core.ModSourceDirPath, sourceDirSubpath)
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
		},
		ExperimentalPrivilegedNesting: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to exec go build in go module sdk container codegen: %w", err)
	}

	return ctr, nil
}

func (sdk *goSDK) base(ctx *core.Context) (*core.Container, error) {
	// TODO: don't hardcode path here, dedupe w/ CI
	tarballPath := "/usr/local/share/dagger/go-module-sdk-image.tar"

	progCtx, recorder := progrock.WithGroup(ctx.Context, "load builtin module sdk go")
	ctx.Context = progCtx
	pbDef, err := sdk.bk.EngineContainerLocalImport(ctx, recorder, sdk.platform, filepath.Dir(tarballPath), nil, []string{filepath.Base(tarballPath)})
	if err != nil {
		return nil, fmt.Errorf("failed to import go module sdk tarball from engine container filesystem: %s", err)
	}
	tarballFile := core.NewFile(ctx, pbDef, filepath.Base(tarballPath), nil, sdk.platform, nil)
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
