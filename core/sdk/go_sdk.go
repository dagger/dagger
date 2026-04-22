package sdk

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/dagger/engine/engineutil"
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

	// goSDKPrebuiltRuntimeRelPath is where a module-source-relative prebuilt
	// runtime binary lives, if any. The engine image's builtin SDK content
	// (toolchains/engine-dev/build/sdk.go::pythonSDKContent) writes the
	// python-sdk runtime here so Runtime() can skip `go build` entirely.
	// The ".dagger-build/" prefix is reserved for build-system outputs and
	// is not part of any user-authored module layout.
	goSDKPrebuiltRuntimeRelPath = ".dagger-build/runtime"

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
	// Go SDK handles type discovery entirely within generate-module
	// (AST scan + schematool merge). The engine falls through to the
	// Runtime + empty-function-name path at asModule time.
	return nil, false
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

	// Modules that opt into skip-codegen-at-runtime commit dagger.gen.go +
	// internal/dagger/** and keep them in sync via explicit `dagger develop`.
	// When generatedContextDirectory is resolved in that mode (e.g. when a
	// downstream module's schema needs the builtin Python SDK's context),
	// re-running codegen would overwrite those committed files with
	// identical bytes and run `go mod tidy` every time. Short-circuit to
	// the existing context directory.
	if !useRuntimeCodegen(source) {
		modName := source.Self().ModuleOriginalName
		contextDir := source.Self().ContextDirectory
		srcSubpath := source.Self().SourceSubpath
		if err := requireGeneratedFiles(ctx, dag, contextDir, srcSubpath, modName); err != nil {
			return nil, err
		}
		return &core.GeneratedCode{
			Code:              contextDir,
			VCSGeneratedPaths: goSDKVCSGeneratedPaths,
			VCSIgnoredPaths:   goSDKVCSIgnoredPaths,
		}, nil
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
		Code:              modifiedSrcDir,
		VCSGeneratedPaths: goSDKVCSGeneratedPaths,
		VCSIgnoredPaths:   goSDKVCSIgnoredPaths,
	}, nil
}

var goSDKVCSGeneratedPaths = []string{
	"dagger.gen.go",
	"internal/dagger/**",
	"internal/querybuilder/**",
	"internal/telemetry/**",
}

var goSDKVCSIgnoredPaths = []string{
	"dagger.gen.go",
	"internal/dagger",
	"internal/querybuilder",
	"internal/telemetry",
	".env", // this is here because the Go SDK does not use WithVCSIgnoredPaths on core/codegen/GeneratedCode
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

	var ctr dagql.ObjectResult[*core.Container]
	var havePrebuilt bool
	if useRuntimeCodegen(source) {
		ctr, err = sdk.baseWithCodegen(ctx, deps, source)
	} else {
		ctr, err = sdk.baseForCommittedCodegen(ctx, source)
		if err == nil {
			havePrebuilt, err = hasPrebuiltRuntime(ctx, dag, source)
		}
	}
	if err != nil {
		return nil, err
	}

	installRuntime := dagql.Selector{
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
	}
	if havePrebuilt {
		// Source is mounted at goSDKUserModContextDirPath by
		// baseForCommittedCodegen; the workdir is the module's source
		// subpath. Copy the prebuilt binary to the well-known runtime
		// path — the binary was compiled for build.platform at engine
		// image build time, so it is safe to run as-is.
		installRuntime = dagql.Selector{
			Field: "withExec",
			Args: []dagql.NamedInput{
				{
					Name: "args",
					Value: dagql.ArrayInput[dagql.String]{
						"cp", goSDKPrebuiltRuntimeRelPath, goSDKRuntimePath,
					},
				},
			},
		}
	}
	if err := dag.Select(ctx, ctr, &ctr,
		installRuntime,
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

// useRuntimeCodegen reports whether this module wants the SDK to run
// codegen during runtime operations (dagger call, dagger functions).
//
// True for modules that haven't opted into the new mode via
// codegen.legacyCodegenAtRuntime=false in dagger.json. This is also
// the default for any module where the field is unset.
func useRuntimeCodegen(src dagql.ObjectResult[*core.ModuleSource]) bool {
	c := src.Self().CodegenConfig
	if c == nil || c.LegacyCodegenAtRuntime == nil {
		return true
	}
	return *c.LegacyCodegenAtRuntime
}

// requireGeneratedFiles ensures the module's committed generated
// files are present when the module has opted out of runtime codegen.
// If either the module's dagger.gen.go or internal/dagger/dagger.gen.go
// is missing, return a clear actionable error.
func requireGeneratedFiles(
	ctx context.Context,
	dag *dagql.Server,
	contextDir dagql.ObjectResult[*core.Directory],
	srcSubpath, modName string,
) error {
	required := []string{
		filepath.Join(srcSubpath, "dagger.gen.go"),
		filepath.Join(srcSubpath, "internal", "dagger", "dagger.gen.go"),
	}
	for _, rel := range required {
		var exists dagql.Boolean
		err := dag.Select(ctx, contextDir, &exists,
			dagql.Selector{
				Field: "exists",
				Args: []dagql.NamedInput{
					{Name: "path", Value: dagql.NewString(rel)},
				},
			},
		)
		if err != nil {
			return fmt.Errorf("check generated file %q: %w", rel, err)
		}
		if !bool(exists) {
			return fmt.Errorf(
				"module %q has codegen.legacyCodegenAtRuntime=false "+
					"but required generated file %q is missing. "+
					"Run `dagger develop` to regenerate.",
				modName, rel)
		}
	}
	return nil
}

// hasPrebuiltRuntime reports whether the module source ships with a
// precompiled Go runtime binary at goSDKPrebuiltRuntimeRelPath. This is
// currently used by the builtin Python SDK (see
// toolchains/engine-dev/build/sdk.go::pythonSDKContent) so the engine
// does not pay the `go build` cost on every `load SDK: python`.
//
// Only meaningful when the module has already opted out of runtime
// codegen; callers must check useRuntimeCodegen first.
func hasPrebuiltRuntime(
	ctx context.Context,
	dag *dagql.Server,
	src dagql.ObjectResult[*core.ModuleSource],
) (bool, error) {
	contextDir := src.Self().ContextDirectory
	srcSubpath := src.Self().SourceSubpath
	rel := filepath.Join(srcSubpath, goSDKPrebuiltRuntimeRelPath)
	var exists dagql.Boolean
	if err := dag.Select(ctx, contextDir, &exists,
		dagql.Selector{
			Field: "exists",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.NewString(rel)},
			},
		},
	); err != nil {
		return false, fmt.Errorf("check prebuilt runtime %q: %w", rel, err)
	}
	return bool(exists), nil
}

// baseForCommittedCodegen prepares the runtime container when the module
// has opted out of runtime codegen. It mounts the module's context
// directory as-is (no withoutFile, no schema JSON, no codegen exec),
// verifies the expected generated files are present, and hands back a
// container ready for `go build`.
func (sdk *goSDK) baseForCommittedCodegen(
	ctx context.Context,
	src dagql.ObjectResult[*core.ModuleSource],
) (dagql.ObjectResult[*core.Container], error) {
	var ctr dagql.ObjectResult[*core.Container]

	dag, err := sdk.root.Server.Server(ctx)
	if err != nil {
		return ctr, fmt.Errorf("failed to get dag for go module sdk runtime: %w", err)
	}

	modName := src.Self().ModuleOriginalName
	contextDir := src.Self().ContextDirectory
	srcSubpath := src.Self().SourceSubpath

	if err := requireGeneratedFiles(ctx, dag, contextDir, srcSubpath, modName); err != nil {
		return ctr, err
	}

	ctr, err = sdk.base(ctx)
	if err != nil {
		return ctr, err
	}

	contextDirID, err := contextDir.ID()
	if err != nil {
		return ctr, fmt.Errorf("failed to get module context directory ID: %w", err)
	}

	if err := dag.Select(ctx, ctr, &ctr,
		dagql.Selector{
			Field: "withMountedDirectory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.NewString(goSDKUserModContextDirPath)},
				{Name: "source", Value: dagql.NewID[*core.Directory](contextDirID)},
			},
		},
		dagql.Selector{
			Field: "withWorkdir",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.NewString(
					filepath.Join(goSDKUserModContextDirPath, srcSubpath))},
			},
		},
	); err != nil {
		return ctr, fmt.Errorf("failed to mount module source: %w", err)
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
