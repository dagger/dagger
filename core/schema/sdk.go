package schema

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"dagger.io/dagger/telemetry"
	"github.com/opencontainers/go-digest"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/distconsts"
)

const (
	runtimeWorkdirPath = "/scratch"
)

type sdkLoader struct {
	dag *dagql.Server
}

func newSDKLoader(dag *dagql.Server) *sdkLoader {
	return &sdkLoader{dag: dag}
}

type SDK string

const (
	SDKGo         SDK = "go"
	SDKPython     SDK = "python"
	SDKTypescript SDK = "typescript"
	SDKPHP        SDK = "php"
	SDKElixir     SDK = "elixir"
	SDKJava       SDK = "java"
)

// this list is to format the invalid sdk msg
// and keeping that in sync with builtinSDK func
var validInbuiltSDKs = []SDK{
	SDKGo,
	SDKPython,
	SDKTypescript,
	SDKPHP,
	SDKElixir,
	SDKJava,
}

// load the SDK implementation with the given name for the module at the given source dir + subpath.
func (s *sdkLoader) sdkForModule(
	ctx context.Context,
	query *core.Query,
	sdk *core.SDKConfig,
	parentSrc *core.ModuleSource,
) (core.SDK, error) {
	if sdk == nil {
		return nil, errors.New("sdk ref is required")
	}

	ctx, span := core.Tracer(ctx).Start(ctx, fmt.Sprintf("sdkForModule: %s", sdk.Source), telemetry.Internal())
	defer span.End()

	builtinSDK, err := s.builtinSDK(ctx, query, sdk)
	if err == nil {
		return builtinSDK, nil
	} else if !errors.Is(err, errUnknownBuiltinSDK) {
		return nil, err
	}

	bk, err := query.Buildkit(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get buildkit for sdk %s: %w", sdk.Source, err)
	}

	sdkModSrc, err := resolveDepToSource(ctx, bk, s.dag, parentSrc, sdk.Source, "", "")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", err.Error(), getInvalidBuiltinSDKError(sdk.Source))
	}
	if !sdkModSrc.Self.ConfigExists {
		return nil, fmt.Errorf("sdk module source has no dagger.json: %w", getInvalidBuiltinSDKError(sdk.Source))
	}

	var sdkMod dagql.Instance[*core.Module]
	err = s.dag.Select(ctx, sdkModSrc, &sdkMod,
		dagql.Selector{Field: "asModule"},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load sdk module %q: %w", sdk.Source, err)
	}

	// TODO: include sdk source dir from module config dagger.json once we support default-args/scripts
	return core.NewModuleSDK(ctx, query, sdkMod, dagql.Instance[*core.Directory]{})
}

// parse and validate the name and version from sdkName
//
// for sdkName with format <sdk-name>@<version>, it returns
// '<sdk-name>' as name and '@<version>' as suffix.
//
// If sdk is one of go/python/typescript and <version>
// is specified, we return an error as those sdk don't support
// specific version
//
// if sdk is one of php/elixir and version is not specified,
// we defaults the version to [engine.Tag]
func parseSDKName(sdkName string) (SDK, string, error) {
	sdkNameParsed, sdkVersion, hasVersion := strings.Cut(sdkName, "@")

	// this validation may seem redundant, but it helps keep the list of
	// builtin sdk between invalidSDKError message and builtinSDK function in sync.
	if !slices.Contains(validInbuiltSDKs, SDK(sdkNameParsed)) {
		return "", "", getInvalidBuiltinSDKError(sdkName)
	}

	// inbuilt sdk go/python/typescript currently does not support selecting a specific version
	if slices.Contains([]SDK{SDKGo, SDKPython, SDKTypescript}, SDK(sdkNameParsed)) && hasVersion {
		return "", "", fmt.Errorf("the %s sdk does not currently support selecting a specific version", sdkNameParsed)
	}

	// for php, elixir we point them to github ref, so default the version to engine's tag
	if slices.Contains([]SDK{SDKPHP, SDKElixir, SDKJava}, SDK(sdkNameParsed)) && sdkVersion == "" {
		sdkVersion = engine.Tag
	}

	sdkSuffix := ""
	if sdkVersion != "" {
		sdkSuffix = "@" + sdkVersion
	}

	return SDK(sdkNameParsed), sdkSuffix, nil
}

var errUnknownBuiltinSDK = fmt.Errorf("unknown builtin sdk")

func getInvalidBuiltinSDKError(inputSDKName string) error {
	inbuiltSDKs := []string{}

	for _, sdk := range validInbuiltSDKs {
		inbuiltSDKs = append(inbuiltSDKs, fmt.Sprintf("- %s", sdk))
	}

	return fmt.Errorf(`%w
The %q SDK does not exist. The available SDKs are:
%s
- any non-bundled SDK from its git ref (e.g. github.com/dagger/dagger/sdk/elixir@main)`,
		errUnknownBuiltinSDK, inputSDKName, strings.Join(inbuiltSDKs, "\n"))
}

// return a builtin SDK implementation with the given name
func (s *sdkLoader) builtinSDK(ctx context.Context, root *core.Query, sdk *core.SDKConfig) (core.SDK, error) {
	sdkNameParsed, sdkSuffix, err := parseSDKName(sdk.Source)
	if err != nil {
		return nil, err
	}

	switch sdkNameParsed {
	case SDKGo:
		return &goSDK{root: root, dag: s.dag}, nil
	case SDKPython:
		return s.loadBuiltinSDK(ctx, root, sdk.Source, digest.Digest(os.Getenv(distconsts.PythonSDKManifestDigestEnvName)))
	case SDKTypescript:
		return s.loadBuiltinSDK(ctx, root, sdk.Source, digest.Digest(os.Getenv(distconsts.TypescriptSDKManifestDigestEnvName)))
	case SDKJava:
		return s.sdkForModule(ctx, root, &core.SDKConfig{Source: "github.com/dagger/dagger/sdk/java" + sdkSuffix}, nil)
	case SDKPHP:
		return s.sdkForModule(ctx, root, &core.SDKConfig{Source: "github.com/dagger/dagger/sdk/php" + sdkSuffix}, nil)
	case SDKElixir:
		return s.sdkForModule(ctx, root, &core.SDKConfig{Source: "github.com/dagger/dagger/sdk/elixir" + sdkSuffix}, nil)
	}

	return nil, getInvalidBuiltinSDKError(sdk.Source)
}

// loadBuiltinSDK loads an SDK implemented as a module that is "builtin" to engine, which means its pre-packaged
// with the engine container in order to enable use w/out hard dependencies on the internet
func (s *sdkLoader) loadBuiltinSDK(
	ctx context.Context,
	root *core.Query,
	name string,
	manifestDigest digest.Digest,
) (core.SDK, error) {
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

	var sdkMod dagql.Instance[*core.Module]
	err := s.dag.Select(ctx, fullSDKDir, &sdkMod,
		dagql.Selector{
			Field: "directory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String("runtime")},
			},
		},
		dagql.Selector{
			Field: "asModuleSource",
		},
		dagql.Selector{
			Field: "asModule",
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to import module sdk %s: %w", name, err)
	}

	return core.NewModuleSDK(ctx, root, sdkMod, fullSDKDir)
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
	dag  *dagql.Server
}

func (sdk *goSDK) AsCodegen(_ context.Context) (core.SDKCodegen, bool, error) {
	return sdk, true, nil
}

func (sdk *goSDK) AsRuntime(_ context.Context) (core.SDKRuntime, bool, error) {
	return sdk, true, nil
}

func (sdk *goSDK) Codegen(
	ctx context.Context,
	deps *core.ModDeps,
	source dagql.Instance[*core.ModuleSource],
) (_ *core.GeneratedCode, rerr error) {
	ctx, span := core.Tracer(ctx).Start(ctx, "go SDK: run codegen")
	defer telemetry.End(span, func() error { return rerr })
	ctr, err := sdk.baseWithCodegen(ctx, deps, source)
	if err != nil {
		return nil, err
	}

	var modifiedSrcDir dagql.Instance[*core.Directory]
	if err := sdk.dag.Select(ctx, ctr, &modifiedSrcDir, dagql.Selector{
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
		},
	}, nil
}

func (sdk *goSDK) Runtime(
	ctx context.Context,
	deps *core.ModDeps,
	source dagql.Instance[*core.ModuleSource],
) (_ *core.Container, rerr error) {
	ctx, span := core.Tracer(ctx).Start(ctx, "go SDK: load runtime")
	defer telemetry.End(span, func() error { return rerr })
	ctr, err := sdk.baseWithCodegen(ctx, deps, source)
	if err != nil {
		return nil, err
	}
	if err := sdk.dag.Select(ctx, ctr, &ctr,
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
					Value: dagql.NewString(runtimeWorkdirPath),
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
	return ctr.Self, nil
}

func (sdk *goSDK) baseWithCodegen(
	ctx context.Context,
	deps *core.ModDeps,
	src dagql.Instance[*core.ModuleSource],
) (dagql.Instance[*core.Container], error) {
	var ctr dagql.Instance[*core.Container]

	schemaJSONFile, err := deps.SchemaIntrospectionJSONFile(ctx)
	if err != nil {
		return ctr, fmt.Errorf("failed to get schema introspection json during module sdk codegen: %w", err)
	}

	modName := src.Self.ModuleOriginalName
	contextDir := src.Self.ContextDirectory
	srcSubpath := src.Self.SourceSubpath

	ctr, err = sdk.base(ctx)
	if err != nil {
		return ctr, err
	}

	// rm dagger.gen.go if it exists, which is going to be overwritten
	// anyways. If it doesn't exist, we ignore not found in the implementation of
	// `withoutFile` so it will be a no-op.
	var updatedContextDir dagql.Instance[*core.Directory]
	if err := sdk.dag.Select(ctx, contextDir, &updatedContextDir,
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

	codegenArgs := dagql.ArrayInput[dagql.String]{
		"--output", dagql.String(goSDKUserModContextDirPath),
		"--module-source-path", dagql.String(filepath.Join(goSDKUserModContextDirPath, srcSubpath)),
		"--module-name", dagql.String(modName),
		"--introspection-json-path", goSDKIntrospectionJSONPath,
	}
	if !src.Self.ConfigExists {
		codegenArgs = append(codegenArgs, "--is-init")
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
					Value: dagql.NewID[*core.File](schemaJSONFile.ID()),
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
					Value: dagql.NewID[*core.Directory](updatedContextDir.ID()),
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

	if err = sdk.dag.Select(ctx, ctr, &ctr, selectors...); err != nil {
		return ctr, fmt.Errorf("failed to mount introspection json file into go module sdk container codegen: %w", err)
	}

	return ctr, nil
}

func (sdk *goSDK) base(ctx context.Context) (dagql.Instance[*core.Container], error) {
	var inst dagql.Instance[*core.Container]

	var baseCtr dagql.Instance[*core.Container]
	if err := sdk.dag.Select(ctx, sdk.dag.Root(), &baseCtr,
		dagql.Selector{
			Field: "builtinContainer",
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

	var modCacheBaseDir dagql.Instance[*core.Directory]
	if err := sdk.dag.Select(ctx, baseCtr, &modCacheBaseDir, dagql.Selector{
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

	var modCache dagql.Instance[*core.CacheVolume]
	if err := sdk.dag.Select(ctx, sdk.dag.Root(), &modCache, dagql.Selector{
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

	var buildCacheBaseDir dagql.Instance[*core.Directory]
	if err := sdk.dag.Select(ctx, baseCtr, &buildCacheBaseDir, dagql.Selector{
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

	var buildCache dagql.Instance[*core.CacheVolume]
	if err := sdk.dag.Select(ctx, sdk.dag.Root(), &buildCache, dagql.Selector{
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

	var ctr dagql.Instance[*core.Container]
	if err := sdk.dag.Select(ctx, baseCtr, &ctr,
		dagql.Selector{
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
				{
					Name:  "source",
					Value: dagql.Opt(dagql.NewID[*core.Directory](modCacheBaseDir.ID())),
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
					Value: dagql.NewID[*core.CacheVolume](buildCache.ID()),
				},
				{
					Name:  "sharing",
					Value: core.CacheSharingModeShared,
				},
				{
					Name:  "source",
					Value: dagql.Opt(dagql.NewID[*core.Directory](buildCacheBaseDir.ID())),
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
