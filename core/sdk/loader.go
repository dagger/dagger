package sdk

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/opencontainers/go-digest"
)

var (
	errMissingSDKRef     = errors.New("sdk ref is required")
	errUnknownBuiltinSDK = errors.New("unknown builtin sdk")
)

type Loader struct{}

func NewLoader() *Loader {
	return &Loader{}
}

// SDKForModule loads an SDK module based on the given SDK configuration.
//
// If it's a builtin SDK, it will load it from the engine container.
// Otherwise, it will load it from the given source either from a URL
// or from a local path.
func (l *Loader) SDKForModule(
	ctx context.Context,
	query *core.Query,
	sdk *core.SDKConfig,
	parentSrc *core.ModuleSource,
) (_ core.SDK, rerr error) {
	if sdk == nil {
		return nil, errMissingSDKRef
	}

	ctx, span := core.Tracer(ctx).Start(ctx, fmt.Sprintf("sdkForModule: %s", sdk.Source), telemetry.Internal())
	defer telemetry.End(span, func() error { return rerr })

	builtinSDK, err := l.namedSDK(ctx, query, sdk)
	if err == nil {
		return builtinSDK, nil
	} else if !errors.Is(err, errUnknownBuiltinSDK) {
		return nil, err
	}

	return l.externalSDKForModule(ctx, query, sdk, parentSrc)
}

// Load an SDK module from an external source (not builtin to the engine).
//
// This will first resolve the path to this SDK module, either from Git
// or from a local path and load it as a module.
func (l *Loader) externalSDKForModule(
	ctx context.Context,
	query *core.Query,
	sdk *core.SDKConfig,
	parentSrc *core.ModuleSource,
) (core.SDK, error) {
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get buildkit for sdk %s: %w", sdk.Source, err)
	}
	dag, err := query.Server.Server(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag for sdk %s: %w", sdk.Source, err)
	}

	sdkModSrc, err := core.ResolveDepToSource(ctx, bk, dag, parentSrc, sdk.Source, "", "")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", err.Error(), getInvalidBuiltinSDKError(sdk.Source))
	}

	if !sdkModSrc.Self().ConfigExists {
		return nil, fmt.Errorf("sdk module source has no dagger.json: %w", getInvalidBuiltinSDKError(sdk.Source))
	}

	var sdkMod dagql.ObjectResult[*core.Module]
	err = dag.Select(ctx, sdkModSrc, &sdkMod,
		dagql.Selector{Field: "asModule", Args: []dagql.NamedInput{
			{Name: "forceDefaultFunctionCaching", Value: dagql.Opt(dagql.Boolean(true))},
		}},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load sdk module %q: %w", sdk.Source, err)
	}

	return newModuleSDK(ctx, query, sdkMod, dagql.ObjectResult[*core.Directory]{}, sdk.Config)
}

func (l *Loader) namedSDK(
	ctx context.Context,
	root *core.Query,
	sdk *core.SDKConfig,
) (core.SDK, error) {
	sdkNamedParsed, sdkSuffix, err := parseSDKName(sdk.Source)
	if err != nil {
		return nil, err
	}

	switch sdkNamedParsed {
	case sdkGo:
		return &goSDK{root: root, rawConfig: sdk.Config}, nil
	case sdkPython:
		return l.loadBuiltinSDK(ctx, root, sdk, digest.Digest(os.Getenv(distconsts.PythonSDKManifestDigestEnvName)))
	case sdkTypescript:
		return l.loadBuiltinSDK(ctx, root, sdk, digest.Digest(os.Getenv(distconsts.TypescriptSDKManifestDigestEnvName)))
	case sdkJava:
		return l.SDKForModule(ctx, root, &core.SDKConfig{Source: "github.com/dagger/dagger/sdk/java" + sdkSuffix, Config: sdk.Config, Experimental: sdk.Experimental}, nil)
	case sdkPHP:
		return l.SDKForModule(ctx, root, &core.SDKConfig{Source: "github.com/dagger/dagger/sdk/php" + sdkSuffix, Config: sdk.Config, Experimental: sdk.Experimental}, nil)
	case sdkElixir:
		return l.SDKForModule(ctx, root, &core.SDKConfig{Source: "github.com/dagger/dagger/sdk/elixir" + sdkSuffix, Config: sdk.Config, Experimental: sdk.Experimental}, nil)
	}

	return nil, getInvalidBuiltinSDKError(sdk.Source)
}

// loads an SDK implemented as a module that is "builtin" to engine, which means its
// pre-packaged with the engine container in order to enable use w/out hard dependencies
// on the internet
func (l *Loader) loadBuiltinSDK(
	ctx context.Context,
	root *core.Query,
	sdk *core.SDKConfig,
	manifestDigest digest.Digest,
) (*module, error) {
	dag, err := root.Server.Server(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag for sdk %s: %w", sdk.Source, err)
	}

	// TODO: currently hardcoding assumption that builtin sdks put *module* source code at
	// "runtime" subdir right under the *full* sdk source dir. Can be generalized once we support
	// default-args/scripts in dagger.json
	var fullSDKDir dagql.ObjectResult[*core.Directory]
	if err := dag.Select(ctx, dag.Root(), &fullSDKDir,
		dagql.Selector{
			Field: "_builtinContainer",
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
		return nil, fmt.Errorf("failed to import full sdk source for sdk %s from engine container filesystem: %w", sdk.Source, err)
	}

	var sdkMod dagql.ObjectResult[*core.Module]
	err = dag.Select(ctx, fullSDKDir, &sdkMod,
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
			Args: []dagql.NamedInput{
				{Name: "forceDefaultFunctionCaching", Value: dagql.Opt(dagql.Boolean(true))},
			},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to import module sdk %s: %w", sdk.Source, err)
	}

	return newModuleSDK(ctx, root, sdkMod, fullSDKDir, sdk.Config)
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
func parseSDKName(sdkName string) (sdk, string, error) {
	sdkNameParsed, sdkVersion, hasVersion := strings.Cut(sdkName, "@")

	// this validation may seem redundant, but it helps keep the list of
	// builtin sdk between invalidSDKError message and builtinSDK function in sync.
	if !slices.Contains(validInbuiltSDKs, sdk(sdkNameParsed)) {
		return "", "", getInvalidBuiltinSDKError(sdkName)
	}

	// inbuilt sdk go/python/typescript currently does not support selecting a specific version
	if slices.Contains([]sdk{sdkGo, sdkPython, sdkTypescript}, sdk(sdkNameParsed)) && hasVersion {
		return "", "", fmt.Errorf("the %s sdk does not currently support selecting a specific version", sdkNameParsed)
	}

	// for php, elixir we point them to github ref, so default the version to engine's tag
	if slices.Contains([]sdk{sdkPHP, sdkElixir, sdkJava}, sdk(sdkNameParsed)) && sdkVersion == "" {
		sdkVersion = engine.Tag
	}

	sdkSuffix := ""
	if sdkVersion != "" {
		sdkSuffix = "@" + sdkVersion
	}

	return sdk(sdkNameParsed), sdkSuffix, nil
}

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
