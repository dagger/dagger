package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/dagger/engine/engineutil"
	"github.com/dagger/dagger/internal/buildkit/identity"
	telemetry "github.com/dagger/otel-go"
	"github.com/mitchellh/mapstructure"
	"github.com/opencontainers/go-digest"
)

const (
	goSDKUserModContextDirPath  = "/src"
	goSDKRuntimePath            = "/runtime"
	goSDKIntrospectionJSONPath  = "/schema.json"
	goSDKDependenciesConfigPath = "/dependencies.json"
	GoSDKModuleIDPath           = "typedefs.json"

	// Set to a commit on https://github.com/dagger/dagger-go-sdk if an unreleased
	// change is needed in the generated library.
	// Otherwise, update it to the latest known commit during release.
	goSDKLibVersion = "7058e9313c720d82c6a07fefb6ce3fab60c7ec4e" // v0.20.6
)

var goSDKExecMDDigest = digest.FromString("go-sdk-with-exec-execmd")

/*
goSDK is the one special sdk not implemented as module, instead the
`cmd/codegen/` binary is packaged into a container w/ the go runtime,
tarball'd up and included in the engine image.

The Codegen and Runtime methods are implemented by loading that tarball and
executing the codegen binary inside it to generate user code and then execute
it with the resulting /runtime binary.
*/
type goSDK struct {
	root      *core.Query
	rawConfig map[string]any
}

type goSDKConfig struct {
	GoPrivate string `json:"goprivate,omitempty"`
}

func (sdk *goSDK) AsRuntime() (core.Runtime, bool) {
	return sdk, true
}

func (sdk *goSDK) AsModuleTypes() (core.ModuleTypes, bool) {
	return sdk, true
}

func (sdk *goSDK) AsCodeGenerator() (core.CodeGenerator, bool) {
	return sdk, true
}

func (sdk *goSDK) AsClientGenerator() (core.ClientGenerator, bool) {
	return sdk, true
}

func (sdk *goSDK) RequiredClientGenerationFiles(_ context.Context) (dagql.Array[dagql.String], error) {
	return dagql.NewStringArray("./go.mod", "./go.sum", "main.go"), nil
}

func (sdk *goSDK) GenerateClient(
	ctx context.Context,
	modSource dagql.ObjectResult[*core.ModuleSource],
	schemaJSONFile dagql.Result[*core.File],
	outputDir string,
) (inst dagql.ObjectResult[*core.Directory], err error) {
	dag, err := sdk.root.Server.Server(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get dag for go module sdk client generation: %w", err)
	}

	modSource, err = scopeSourceForSDKOperation(ctx, modSource, "generateClient", dag)
	if err != nil {
		return inst, fmt.Errorf("failed to scope module source for go module sdk client generation: %w", err)
	}
	ctr, err := sdk.base(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get base container during module client generation: %w", err)
	}

	contextDir := modSource.Self().ContextDirectory
	rootSourcePath := modSource.Self().SourceRootSubpath

	modSourceIDHandle, err := modSource.ID()
	if err != nil {
		return inst, fmt.Errorf("failed to get module source id: %w", err)
	}
	modSourceID, err := modSourceIDHandle.Encode()
	if err != nil {
		return inst, fmt.Errorf("failed to get module source id: %w", err)
	}
	schemaJSONFileID, err := schemaJSONFile.ID()
	if err != nil {
		return inst, fmt.Errorf("failed to get schema introspection json ID: %w", err)
	}
	contextDirID, err := contextDir.ID()
	if err != nil {
		return inst, fmt.Errorf("failed to get module context directory ID: %w", err)
	}

	codegenArgs := dagql.ArrayInput[dagql.String]{
		"generate-client",
		"--output", dagql.String(filepath.Join(goSDKUserModContextDirPath, rootSourcePath)),
		"--introspection-json-path", goSDKIntrospectionJSONPath,
		dagql.String(fmt.Sprintf("--module-source-id=%s", modSourceID)),
		dagql.String(fmt.Sprintf("--client-dir=%s", outputDir)),
	}

	err = dag.Select(ctx, ctr, &ctr,
		dagql.Selector{
			Field: "withMountedFile",
			Args: []dagql.NamedInput{
				{
					Name:  "path",
					Value: dagql.NewString(goSDKIntrospectionJSONPath),
				},
				{
					Name:  "source",
					Value: dagql.NewID[*core.File](schemaJSONFileID),
				},
			},
		},
		dagql.Selector{
			Field: "withoutDefaultArgs",
		},
		dagql.Selector{
			Field: "withMountedDirectory",
			Args: []dagql.NamedInput{
				{
					Name:  "path",
					Value: dagql.String(goSDKUserModContextDirPath),
				},
				{
					Name:  "source",
					Value: dagql.NewID[*core.Directory](contextDirID),
				},
			},
		},
		dagql.Selector{
			Field: "withWorkdir",
			Args: []dagql.NamedInput{
				{
					Name:  "path",
					Value: dagql.NewString(filepath.Join(goSDKUserModContextDirPath, rootSourcePath)),
				},
			},
		},
		dagql.Selector{
			Field: "withExec",
			Args: []dagql.NamedInput{
				{
					Name: "args",
					Value: append(dagql.ArrayInput[dagql.String]{
						"codegen",
					}, codegenArgs...),
				},
				{
					Name:  "experimentalPrivilegedNesting",
					Value: dagql.NewBoolean(true),
				},
			},
		},
	)
	if err != nil {
		return inst, fmt.Errorf("failed to run  module client generation: %w", err)
	}

	var modifiedSrcDir dagql.ObjectResult[*core.Directory]
	if err := dag.Select(ctx, ctr, &modifiedSrcDir, dagql.Selector{
		Field: "directory",
		Args: []dagql.NamedInput{
			{
				Name:  "path",
				Value: dagql.String(goSDKUserModContextDirPath),
			},
		},
	}); err != nil {
		return inst, fmt.Errorf("failed to get modified source directory for go module sdk codegen: %w", err)
	}

	return modifiedSrcDir, nil
}

func (sdk *goSDK) Codegen(
	ctx context.Context,
	deps *core.SchemaBuilder,
	source dagql.ObjectResult[*core.ModuleSource],
) (_ *core.GeneratedCode, rerr error) {
	ctx, span := core.Tracer(ctx).Start(ctx, "go SDK: run codegen")
	defer telemetry.EndWithCause(span, &rerr)

	dag, err := sdk.root.Server.Server(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag for go module sdk codegen: %w", err)
	}

	source, err = scopeSourceForSDKOperation(ctx, source, "codegen", dag)
	if err != nil {
		return nil, fmt.Errorf("failed to scope module source for go module sdk codegen: %w", err)
	}

	ctr, err := sdk.baseWithCodegen(ctx, deps, source)
	if err != nil {
		return nil, err
	}

	var modifiedSrcDir dagql.ObjectResult[*core.Directory]
	if err := dag.Select(ctx, ctr, &modifiedSrcDir, dagql.Selector{
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
			"internal/telemetry/**",
		},
		VCSIgnoredPaths: []string{
			"dagger.gen.go",
			"internal/dagger",
			"internal/querybuilder",
			"internal/telemetry",
			".env", // this is here because the Go SDK does not use WithVCSIgnoredPaths on core/codegen/GeneratedCode
		},
	}, nil
}

func (sdk *goSDK) ModuleTypes(
	ctx context.Context,
	deps *core.SchemaBuilder,
	src dagql.ObjectResult[*core.ModuleSource],
	partiallyInitializedMod *core.Module,
) (inst dagql.ObjectResult[*core.Module], rerr error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get dag for go module sdk codegen: %w", err)
	}

	src, err = scopeSourceForSDKOperation(ctx, src, "moduleTypes", dag)
	if err != nil {
		return inst, fmt.Errorf("failed to scope module source for go module sdk module types: %w", err)
	}

	schemaJSONFile, err := deps.SchemaIntrospectionJSONFileForModule(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get schema introspection json during module client generation: %w", err)
	}
	scopedMod, err := ScopeModuleForSDKOperation(ctx, partiallyInitializedMod, "goSDK", dag)
	if err != nil {
		return inst, fmt.Errorf("failed to scope module for go module sdk module types: %w", err)
	}
	currentModuleID, err := scopedMod.ID()
	if err != nil {
		return inst, fmt.Errorf("failed to get current module ID for go module sdk module types: %w", err)
	}

	var ctr dagql.ObjectResult[*core.Container]

	modName := src.Self().ModuleOriginalName
	contextDir := src.Self().ContextDirectory
	srcSubpath := src.Self().SourceSubpath
	schemaJSONFileID, err := schemaJSONFile.ID()
	if err != nil {
		return inst, fmt.Errorf("failed to get schema introspection json ID: %w", err)
	}
	contextDirID, err := contextDir.ID()
	if err != nil {
		return inst, fmt.Errorf("failed to get module context directory ID: %w", err)
	}

	ctr, err = sdk.base(ctx)
	if err != nil {
		return inst, err
	}

	execMD := engineutil.ExecutionMetadata{
		ClientID: identity.NewID(),
		Call:     dagql.CurrentCall(ctx),
		ExecID:   identity.NewID(),
		Internal: true,
	}
	if execMD.Call != nil {
		callDigest, err := execMD.Call.RecipeDigest(ctx)
		if err != nil {
			return inst, fmt.Errorf("compute Go SDK exec call digest: %w", err)
		}
		execMD.CallDigest = callDigest
	}
	execMD.EncodedModuleID, err = currentModuleID.Encode()
	if err != nil {
		return inst, err
	}

	err = dag.Select(ctx, ctr, &ctr,
		dagql.Selector{
			Field: "withMountedFile",
			Args: []dagql.NamedInput{
				{
					Name:  "path",
					Value: dagql.NewString(goSDKIntrospectionJSONPath),
				},
				{
					Name:  "source",
					Value: dagql.NewID[*core.File](schemaJSONFileID),
				},
			},
		},
		dagql.Selector{
			Field: "withMountedDirectory",
			Args: []dagql.NamedInput{
				{
					Name:  "path",
					Value: dagql.NewString(goSDKUserModContextDirPath),
				},
				{
					Name:  "source",
					Value: dagql.NewID[*core.Directory](contextDirID),
				},
			},
		},
		dagql.Selector{
			Field: "withoutFile",
			Args: []dagql.NamedInput{
				{
					Name:  "path",
					Value: dagql.String(filepath.Join(goSDKUserModContextDirPath, srcSubpath, "dagger.gen.go")),
				},
			},
		},
		dagql.Selector{
			Field: "withoutDirectory",
			Args: []dagql.NamedInput{
				{
					Name:  "path",
					Value: dagql.String(filepath.Join(goSDKUserModContextDirPath, srcSubpath, "internal")),
				},
			},
		},
		dagql.Selector{
			Field: "withWorkdir",
			Args: []dagql.NamedInput{
				{
					Name:  "path",
					Value: dagql.NewString(filepath.Join(goSDKUserModContextDirPath, srcSubpath)),
				},
			},
		},
		dagql.Selector{
			Field: "withExec",
			Args: []dagql.NamedInput{
				{
					Name: "args",
					Value: dagql.ArrayInput[dagql.String]{
						"codegen",
						"generate-typedefs",
						"--module-source-path", dagql.String(filepath.Join(goSDKUserModContextDirPath, srcSubpath)),
						"--module-name", dagql.String(modName),
						"--introspection-json-path", goSDKIntrospectionJSONPath,
						"--lib-version", dagql.String(goSDKLibVersion),
						"--output", GoSDKModuleIDPath,
					},
				},
				{
					Name:  "experimentalPrivilegedNesting",
					Value: dagql.Boolean(true),
				},
				{
					Name:  "execMD",
					Value: dagql.NewDigestedSerializedString(&execMD, goSDKExecMDDigest),
				},
			},
		},
	)
	if err != nil {
		return inst, fmt.Errorf("failed to run go module type defs generation: %w", err)
	}

	var syncedCtrID dagql.ID[*core.Container]
	if err = dag.Select(ctx, ctr, &syncedCtrID, dagql.Selector{
		Field: "sync",
	}); err != nil {
		return inst, fmt.Errorf("failed to sync go module type defs generation container: %w", err)
	}

	ctr, err = syncedCtrID.Load(ctx, dag)
	if err != nil {
		return inst, fmt.Errorf("failed to load synced go module type defs generation container: %w", err)
	}

	var modDefsID string
	err = dag.Select(ctx, ctr, &modDefsID,
		dagql.Selector{
			Field: "file",
			Args: []dagql.NamedInput{
				{
					Name:  "path",
					Value: dagql.NewString(GoSDKModuleIDPath),
				},
			},
		},
		dagql.Selector{
			Field: "contents",
		},
	)
	if err != nil {
		return inst, fmt.Errorf("failed to get type defs json during module sdk codegen: %w", err)
	}

	modCallID := new(call.ID)
	if err = json.Unmarshal([]byte(modDefsID), modCallID); err != nil {
		return inst, fmt.Errorf("failed to decode module call ID from type defs json: %w", err)
	}

	inst, err = dagql.NewID[*core.Module](modCallID).Load(ctx, dag)
	if err != nil {
		return inst, fmt.Errorf("failed to load module from type defs json: %w", err)
	}
	// generate-typedefs emits a handle-form module ID out of the withExec result.
	// Retain that loaded module under the producing exec container so it cannot be
	// pruned while the exec result that created it is still live.
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get engine cache for go type defs dependency: %w", err)
	}
	if err := cache.AddExplicitDependency(ctx, ctr, inst, "go_sdk_generate_typedefs"); err != nil {
		return inst, fmt.Errorf("failed to retain generated module result from go type defs exec: %w", err)
	}

	return inst, nil
}

func (sdk *goSDK) Runtime(
	ctx context.Context,
	deps *core.SchemaBuilder,
	source dagql.ObjectResult[*core.ModuleSource],
) (_ core.ModuleRuntime, rerr error) {
	ctx, span := core.Tracer(ctx).Start(ctx, "go SDK: load runtime")
	defer telemetry.EndWithCause(span, &rerr)

	dag, err := sdk.root.Server.Server(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag for go module sdk runtime: %w", err)
	}

	source, err = scopeSourceForSDKOperation(ctx, source, "runtime", dag)
	if err != nil {
		return nil, fmt.Errorf("failed to scope module source for go module sdk runtime: %w", err)
	}

	ctr, err := sdk.baseWithCodegen(ctx, deps, source)
	if err != nil {
		return nil, err
	}
	if err := dag.Select(ctx, ctr, &ctr,
		dagql.Selector{
			Field: "withExec",
			Args: []dagql.NamedInput{
				{
					Name: "args",
					Value: dagql.ArrayInput[dagql.String]{
						"go", "build",
						"-ldflags", "-s -w", // strip DWARF debug symbols to save a few MBs of space
						"-o", goSDKRuntimePath,
						".",
					},
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
					Value: dagql.NewString(RuntimeWorkdirPath),
				},
			},
		},
		// remove shared cache mounts from final container so module code can't
		// do weird things with them like IPC, etc.
		dagql.Selector{
			Field: "withoutMount",
			Args: []dagql.NamedInput{
				{
					Name:  "path",
					Value: dagql.String("/go/pkg/mod"),
				},
			},
		},
		dagql.Selector{
			Field: "withoutMount",
			Args: []dagql.NamedInput{
				{
					Name:  "path",
					Value: dagql.String("/root/.cache/go-build"),
				},
			},
		},
	); err != nil {
		return nil, fmt.Errorf("failed to build go runtime binary: %w", err)
	}

	if cfg := source.Self().SDK; cfg != nil && cfg.Debug {
		if err := dag.Select(ctx, ctr, &ctr, dagql.Selector{Field: "terminal"}); err != nil {
			return nil, fmt.Errorf("failed to enable go sdk runtime terminal: %w", err)
		}
	}

	return &core.ContainerRuntime{Container: ctr}, nil
}

func (sdk *goSDK) baseWithCodegen(
	ctx context.Context,
	deps *core.SchemaBuilder,
	src dagql.ObjectResult[*core.ModuleSource],
) (dagql.ObjectResult[*core.Container], error) {
	var ctr dagql.ObjectResult[*core.Container]

	dag, err := sdk.root.Server.Server(ctx)
	if err != nil {
		return ctr, fmt.Errorf("failed to get dag for go module sdk codegen: %w", err)
	}

	schemaJSONFile, err := deps.SchemaIntrospectionJSONFileForModule(ctx)
	if err != nil {
		return ctr, fmt.Errorf("failed to get schema introspection json during module sdk codegen: %w", err)
	}
	schemaJSONFileID, err := schemaJSONFile.ID()
	if err != nil {
		return ctr, fmt.Errorf("failed to get schema introspection json ID during module sdk codegen: %w", err)
	}

	modName := src.Self().ModuleOriginalName
	contextDir := src.Self().ContextDirectory
	srcSubpath := src.Self().SourceSubpath

	ctr, err = sdk.base(ctx)
	if err != nil {
		return ctr, err
	}

	// rm dagger.gen.go if it exists, which is going to be overwritten
	// anyways. If it doesn't exist, we ignore not found in the implementation of
	// `withoutFile` so it will be a no-op.
	var updatedContextDir dagql.Result[*core.Directory]
	if err := dag.Select(ctx, contextDir, &updatedContextDir,
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
	updatedContextDirID, err := updatedContextDir.ID()
	if err != nil {
		return ctr, fmt.Errorf("failed to get updated context directory ID during module sdk codegen: %w", err)
	}

	codegenArgs := dagql.ArrayInput[dagql.String]{
		"generate-module",
		"--output", dagql.String(goSDKUserModContextDirPath),
		"--module-source-path", dagql.String(filepath.Join(goSDKUserModContextDirPath, srcSubpath)),
		"--module-name", dagql.String(modName),
		"--introspection-json-path", goSDKIntrospectionJSONPath,
		"--lib-version", dagql.String(goSDKLibVersion),
	}
	if !src.Self().ConfigExists {
		codegenArgs = append(codegenArgs, "--is-init")
	}
	if sdkCfg := src.Self().SDK; sdkCfg != nil &&
		sdkCfg.ExperimentalFeatureEnabled(core.ModuleSourceExperimentalFeatureSelfCalls) {
		codegenArgs = append(codegenArgs, "--self-calls")
	}

	selectors := []dagql.Selector{
		{
			Field: "withMountedFile",
			Args: []dagql.NamedInput{
				{
					Name:  "path",
					Value: dagql.NewString(goSDKIntrospectionJSONPath),
				},
				{
					Name:  "source",
					Value: dagql.NewID[*core.File](schemaJSONFileID),
				},
			},
		},
		{
			Field: "withMountedDirectory",
			Args: []dagql.NamedInput{
				{
					Name:  "path",
					Value: dagql.NewString(goSDKUserModContextDirPath),
				},
				{
					Name:  "source",
					Value: dagql.NewID[*core.Directory](updatedContextDirID),
				},
			},
		},
		{
			Field: "withWorkdir",
			Args: []dagql.NamedInput{
				{
					Name:  "path",
					Value: dagql.NewString(filepath.Join(goSDKUserModContextDirPath, srcSubpath)),
				},
			},
		},
	}

	var config goSDKConfig
	var mapstructureMetadata mapstructure.Metadata
	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Metadata: &mapstructureMetadata,
		Result:   &config,
	})
	if err != nil {
		return ctr, err
	}

	err = decoder.Decode(sdk.rawConfig)
	if err != nil {
		return ctr, err
	}

	if len(mapstructureMetadata.Unused) > 0 {
		return ctr, fmt.Errorf("unknown sdk config keys found %v", mapstructureMetadata.Unused)
	}

	configSelectors := getSDKConfigSelectors(ctx, config)
	selectors = append(selectors, configSelectors...)

	// fetch gitconfig selectors
	bk, err := sdk.root.Engine(ctx)
	if err != nil {
		return ctr, err
	}

	gitConfigSelectors, err := gitConfigSelectors(ctx, bk)
	if err != nil {
		return ctr, err
	}
	selectors = append(selectors, gitConfigSelectors...)

	// TODO(rajatjindal): verify with Erik as to why this
	// cause failures if we also mount this in Runtime.
	// Issue we run into is that when we try to run sdk checks
	// using .dagger, it fails trying to find the socket
	setSSHAuthSelectors, unsetSSHAuthSelectors, err := sdk.getUnixSocketSelector(ctx)
	if err != nil {
		return ctr, err
	}
	selectors = append(selectors, setSSHAuthSelectors...)

	// now that we are done with gitconfig and injecting env
	// variables, we can run the codegen command.
	selectors = append(selectors,
		dagql.Selector{
			Field: "withoutDefaultArgs",
		},
		dagql.Selector{
			Field: "withExec",
			Args: []dagql.NamedInput{
				{
					Name: "args",
					Value: append(dagql.ArrayInput[dagql.String]{
						"codegen",
					}, codegenArgs...),
				},
			},
		},
	)

	selectors = append(selectors, unsetSSHAuthSelectors...)

	if err := dag.Select(ctx, ctr, &ctr, selectors...); err != nil {
		return ctr, fmt.Errorf("failed to mount introspection json file into go module sdk container codegen: %w", err)
	}

	return ctr, nil
}

func (sdk *goSDK) base(ctx context.Context) (dagql.ObjectResult[*core.Container], error) {
	var inst dagql.ObjectResult[*core.Container]

	dag, err := sdk.root.Server.Server(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get dag for go module sdk client generation: %w", err)
	}

	var baseCtr dagql.ObjectResult[*core.Container]
	if err := dag.Select(ctx, dag.Root(), &baseCtr,
		dagql.Selector{
			Field: "_builtinContainer",
			Args: []dagql.NamedInput{
				{
					Name:  "digest",
					Value: dagql.String(os.Getenv(distconsts.GoSDKManifestDigestEnvName)),
				},
			},
		},
	); err != nil {
		return inst, fmt.Errorf("failed to get base container from go module sdk tarball: %w", err)
	}

	var modCacheBaseDir dagql.Result[*core.Directory]
	if err := dag.Select(ctx, baseCtr, &modCacheBaseDir, dagql.Selector{
		Field: "directory",
		Args: []dagql.NamedInput{
			{
				Name:  "path",
				Value: dagql.String("/go/pkg/mod"),
			},
		},
	}); err != nil {
		return inst, fmt.Errorf("failed to get mod cache base dir from go module sdk tarball: %w", err)
	}

	var modCache dagql.Result[*core.CacheVolume]
	if err := dag.Select(ctx, dag.Root(), &modCache, dagql.Selector{
		Field: "cacheVolume",
		Args: []dagql.NamedInput{
			{
				Name:  "key",
				Value: dagql.String("gomod"),
			},
			{
				Name:  "namespace",
				Value: dagql.String("internal"),
			},
		},
	}); err != nil {
		return inst, fmt.Errorf("failed to get mod cache from go module sdk tarball: %w", err)
	}

	var buildCacheBaseDir dagql.Result[*core.Directory]
	if err := dag.Select(ctx, baseCtr, &buildCacheBaseDir, dagql.Selector{
		Field: "directory",
		Args: []dagql.NamedInput{
			{
				Name:  "path",
				Value: dagql.String("/root/.cache/go-build"),
			},
		},
	}); err != nil {
		return inst, fmt.Errorf("failed to get build cache base dir from go module sdk tarball: %w", err)
	}

	var buildCache dagql.Result[*core.CacheVolume]
	if err := dag.Select(ctx, dag.Root(), &buildCache, dagql.Selector{
		Field: "cacheVolume",
		Args: []dagql.NamedInput{
			{
				Name:  "key",
				Value: dagql.String("gobuild"),
			},
			{
				Name:  "namespace",
				Value: dagql.String("internal"),
			},
		},
	}); err != nil {
		return inst, fmt.Errorf("failed to get build cache from go module sdk tarball: %w", err)
	}
	modCacheID, err := modCache.ID()
	if err != nil {
		return inst, fmt.Errorf("failed to get module cache ID from go module sdk tarball: %w", err)
	}
	modCacheBaseDirID, err := modCacheBaseDir.ID()
	if err != nil {
		return inst, fmt.Errorf("failed to get module cache base dir ID from go module sdk tarball: %w", err)
	}
	buildCacheID, err := buildCache.ID()
	if err != nil {
		return inst, fmt.Errorf("failed to get build cache ID from go module sdk tarball: %w", err)
	}
	buildCacheBaseDirID, err := buildCacheBaseDir.ID()
	if err != nil {
		return inst, fmt.Errorf("failed to get build cache base dir ID from go module sdk tarball: %w", err)
	}

	var ctr dagql.ObjectResult[*core.Container]
	if err := dag.Select(ctx, baseCtr, &ctr,
		dagql.Selector{
			Field: "withMountedCache",
			Args: []dagql.NamedInput{
				{
					Name:  "path",
					Value: dagql.String("/go/pkg/mod"),
				},
				{
					Name:  "cache",
					Value: dagql.NewID[*core.CacheVolume](modCacheID),
				},
				{
					Name:  "sharing",
					Value: core.CacheSharingModeShared,
				},
				{
					Name:  "source",
					Value: dagql.Opt(dagql.NewID[*core.Directory](modCacheBaseDirID)),
				},
			},
		},
		dagql.Selector{
			Field: "withMountedCache",
			Args: []dagql.NamedInput{
				{
					Name:  "path",
					Value: dagql.String("/root/.cache/go-build"),
				},
				{
					Name:  "cache",
					Value: dagql.NewID[*core.CacheVolume](buildCacheID),
				},
				{
					Name:  "sharing",
					Value: core.CacheSharingModeShared,
				},
				{
					Name:  "source",
					Value: dagql.Opt(dagql.NewID[*core.Directory](buildCacheBaseDirID)),
				},
			},
		},
		dagql.Selector{
			Field: "__withSystemEnvVariable",
			Args: []dagql.NamedInput{
				{
					Name:  "name",
					Value: dagql.String("GOPROXY"),
				},
			},
		},
		dagql.Selector{
			Field: "__withSystemEnvVariable",
			Args: []dagql.NamedInput{
				{
					Name:  "name",
					Value: dagql.String("GODEBUG"),
				},
			},
		},
	); err != nil {
		return inst, fmt.Errorf("failed to get container from go module sdk tarball: %w", err)
	}
	return ctr, nil
}

func gitConfigSelectors(ctx context.Context, bk *engineutil.Client) ([]dagql.Selector, error) {
	// codegen runs `go mod tidy` and for private deps
	// we allow users to configure GOPRIVATE env variable.
	// But for it to work, we need to ensure we don't run into
	// host checking prompt. So customizing GIT_SSH_COMMAND to
	// allow skipping the prompt.
	selectors := []dagql.Selector{
		{
			Field: "withEnvVariable",
			Args: []dagql.NamedInput{
				{
					Name:  "name",
					Value: dagql.NewString("GIT_SSH_COMMAND"),
				},
				{
					Name:  "value",
					Value: dagql.NewString("ssh -o StrictHostKeyChecking=no "),
				},
			},
		},
	}

	gitconfig, err := bk.GetGitConfig(ctx)
	if err != nil {
		return nil, err
	}

	for _, entry := range gitconfig {
		selectors = append(selectors,
			dagql.Selector{
				Field: "withExec",
				Args: []dagql.NamedInput{
					{
						Name: "args",
						Value: dagql.ArrayInput[dagql.String]{
							"git", "config", "--global", dagql.NewString(entry.Key), dagql.NewString(entry.Value),
						},
					},
				},
			})
	}

	return selectors, nil
}

func (sdk *goSDK) getUnixSocketSelector(ctx context.Context) ([]dagql.Selector, []dagql.Selector, error) {
	dag, err := sdk.root.Server.Server(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get dag for go module sdk: %w", err)
	}

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get client metadata from context: %w", err)
	}

	if clientMetadata.SSHAuthSocketPath == "" {
		return nil, nil, nil
	}

	var sockInst dagql.Result[*core.Socket]
	if err := dag.Select(ctx, dag.Root(), &sockInst,
		dagql.Selector{
			Field: "host",
		},
		dagql.Selector{
			Field: "_sshAuthSocket",
		},
	); err != nil {
		return nil, nil, fmt.Errorf("failed to select internal socket: %w", err)
	}

	if sockInst.Self() == nil {
		return nil, nil, fmt.Errorf("sockInst.Self is NIL")
	}
	sockInstID, err := sockInst.ID()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get ssh socket ID: %w", err)
	}

	sshSockPath := "/tmp/dagger-ssh-sock"
	set := []dagql.Selector{
		{
			Field: "withUnixSocket",
			Args: []dagql.NamedInput{
				{
					Name:  "path",
					Value: dagql.String(sshSockPath),
				},
				{
					Name:  "source",
					Value: dagql.NewID[*core.Socket](sockInstID),
				},
			},
		},
		{
			Field: "withEnvVariable",
			Args: []dagql.NamedInput{
				{
					Name:  "name",
					Value: dagql.NewString("SSH_AUTH_SOCK"),
				},
				{
					Name:  "value",
					Value: dagql.String(sshSockPath),
				},
			},
		},
	}
	unset := []dagql.Selector{
		{
			Field: "withoutUnixSocket",
			Args: []dagql.NamedInput{
				{
					Name:  "path",
					Value: dagql.NewString(sshSockPath),
				},
			},
		},
		{
			Field: "withoutEnvVariable",
			Args: []dagql.NamedInput{
				{
					Name:  "name",
					Value: dagql.NewString("SSH_AUTH_SOCK"),
				},
			},
		},
	}
	return set, unset, nil
}

func getSDKConfigSelectors(_ context.Context, config goSDKConfig) []dagql.Selector {
	var selectors []dagql.Selector
	if config.GoPrivate != "" {
		selectors = append(selectors, dagql.Selector{
			Field: "withEnvVariable",
			Args: []dagql.NamedInput{
				{
					Name:  "name",
					Value: dagql.NewString("GOPRIVATE"),
				},
				{
					Name:  "value",
					Value: dagql.NewString(config.GoPrivate),
				},
			},
		})
	}

	return selectors
}
