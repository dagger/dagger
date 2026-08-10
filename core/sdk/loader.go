package sdk

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/sdk/sdkmeta"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/distconsts"
	iversion "github.com/dagger/dagger/internal/version"
	telemetry "github.com/dagger/otel-go"
	"github.com/opencontainers/go-digest"
)

var (
	errMissingSDKRef     = errors.New("no sdk ref provided")
	errUnknownBuiltinSDK = errors.New("unknown built-in sdk")
)

type Loader struct{}

func NewLoader() *Loader {
	return &Loader{}
}

func init() {
	core.SetModuleSourceSDKLoader(func(ctx context.Context, query *core.Query, sdkCfg *core.SDKConfig, src *core.ModuleSource) (core.SDK, error) {
		return NewLoader().SDKForModule(ctx, query, sdkCfg, src)
	})
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

	ctx, span := core.Tracer(ctx).Start(ctx, fmt.Sprintf("load SDK: %s", sdk.Source))
	defer telemetry.EndWithCause(span, &rerr)

	builtinSDK, builtinErr := l.namedSDK(ctx, query, sdk)
	if builtinErr == nil {
		return builtinSDK, nil
	} else if !errors.Is(builtinErr, errUnknownBuiltinSDK) {
		return nil, builtinErr
	}

	extSDK, extErr := l.externalSDKForModule(ctx, query, sdk, parentSrc)
	if extErr == nil {
		return extSDK, nil
	}

	stdio := telemetry.SpanStdio(ctx, "dagger.io/core/sdk")
	fmt.Fprintf(stdio.Stderr, "Could not load SDK %q.\n", sdk.Source)
	fmt.Fprintln(stdio.Stderr)
	fmt.Fprintln(stdio.Stderr, "Errors:")
	fmt.Fprintln(stdio.Stderr, "-", builtinErr)
	fmt.Fprintln(stdio.Stderr, "-", extErr)
	fmt.Fprintln(stdio.Stderr)
	fmt.Fprintln(stdio.Stderr, "The available SDKs are:")
	for _, sdk := range sdkmeta.Builtins {
		fmt.Fprintln(stdio.Stderr, "-", sdk)
	}
	fmt.Fprintln(stdio.Stderr, "- any git module ref, e.g. github.com/dagger/dagger/sdk/elixir@main")
	fmt.Fprintln(stdio.Stderr, "- any local module path, e.g. ./my-sdk")

	return nil, fmt.Errorf("invalid SDK: %q", sdk.Source)
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
	bk, err := query.Engine(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get engine client for sdk %s: %w", sdk.Source, err)
	}
	dag, err := query.Server.Server(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag for sdk %s: %w", sdk.Source, err)
	}

	sdkModSrc, err := core.ResolveDepToSource(ctx, bk, dag, parentSrc, sdk.Source, "", "")
	if err != nil {
		return nil, err
	}

	if !sdkModSrc.Self().ConfigExists {
		return nil, fmt.Errorf("sdk module source has no dagger.json")
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
	case sdkDang:
		return &dangSDK{root: root, rawConfig: sdk.Config}, nil
	case sdkPython:
		return l.loadBuiltinSDK(ctx, root, sdk, digest.Digest(os.Getenv(distconsts.PythonSDKManifestDigestEnvName)))
	case sdkTypescript:
		return l.loadBuiltinSDK(ctx, root, sdk, digest.Digest(os.Getenv(distconsts.TypescriptSDKManifestDigestEnvName)))
	case sdkRust:
		manifestDigest, err := rustSDKManifestDigest()
		if err != nil {
			return nil, err
		}
		if _, err := rustSDKDescriptorDigest(); err != nil {
			return nil, err
		}
		return l.loadBuiltinSDK(ctx, root, sdk, manifestDigest)
	case sdkJava, sdkPHP, sdkElixir:
		sdkMod, ok := workspaceModuleForBuiltinSDK(sdkNamedParsed, sdkSuffix)
		if !ok {
			return nil, errUnknownBuiltinSDK
		}
		sdkConfig := &core.SDKConfig{
			Source:       sdkMod.Source,
			Config:       sdk.Config,
			Experimental: sdk.Experimental,
		}
		loaded, tagErr := l.externalSDKForModule(ctx, root, sdkConfig, nil)
		if tagErr == nil {
			return loaded, nil
		}

		// A bare remote builtin normally resolves at engine.Tag. On main,
		// VERSION already names the next release before its Git tag exists.
		// Retry at the exact engine source commit, which is always available
		// for provenance-stamped builds. Explicit user refs remain authoritative.
		// TODO(https://github.com/dagger/dagger/issues/13755): Since these
		// runtimes live in this repository, should bare refs always resolve from
		// the engine commit instead of pulling by tag first?
		_, _, hasExplicitVersion := strings.Cut(sdk.Source, "@")
		if hasExplicitVersion ||
			!errors.Is(tagErr, core.ErrModuleVersionNotFound) ||
			iversion.Commit == "" {
			return nil, tagErr
		}
		commitMod, ok := workspaceModuleForBuiltinSDK(sdkNamedParsed, "@"+iversion.Commit)
		if !ok {
			return nil, errUnknownBuiltinSDK
		}
		sdkConfig.Source = commitMod.Source
		loaded, commitErr := l.externalSDKForModule(ctx, root, sdkConfig, nil)
		if commitErr != nil {
			return nil, fmt.Errorf(
				"failed to load SDK %q from %q: %w; fallback to engine commit %q failed: %w",
				sdk.Source,
				sdkMod.Source,
				tagErr,
				iversion.Commit,
				commitErr,
			)
		}
		return loaded, nil
	}

	return nil, errUnknownBuiltinSDK
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

	moduleSource := []dagql.Selector{{Field: "directory", Args: []dagql.NamedInput{
		{Name: "path", Value: dagql.String("runtime")},
	}}, {Field: "asModuleSource"}}
	if sdk.Source == string(sdkRust) {
		// Rust's installed-SDK generators execute through the module itself, not
		// through the hook instance that receives optionalFullSDKSourceDir. Keep
		// the sealed content root as their +defaultPath context while selecting
		// runtime/ as the implementation root.
		moduleSource = []dagql.Selector{{
			Field: "asModuleSource",
			Args: []dagql.NamedInput{
				{Name: "sourceRootPath", Value: dagql.String("runtime")},
			},
		}}
	}
	var sdkMod dagql.ObjectResult[*core.Module]
	err = dag.Select(ctx, fullSDKDir, &sdkMod,
		append(moduleSource,
			dagql.Selector{
				Field: "asModule",
				Args: []dagql.NamedInput{
					{Name: "forceDefaultFunctionCaching", Value: dagql.Opt(dagql.Boolean(true))},
				},
			},
		)...,
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
	if !sdkmeta.IsBuiltin(sdkNameParsed) {
		return "", "", errUnknownBuiltinSDK
	}

	// inbuilt sdk go/python/typescript currently does not support selecting a specific version
	if slices.Contains([]sdk{sdkGo, sdkDang, sdkPython, sdkTypescript, sdkRust}, sdk(sdkNameParsed)) && hasVersion {
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

func rustSDKManifestDigest() (digest.Digest, error) {
	manifestDigest := digest.Digest(os.Getenv(distconsts.RustSDKManifestDigestEnvName))
	if err := manifestDigest.Validate(); err != nil {
		return "", fmt.Errorf("rust SDK provenance: packaged manifest digest is absent or malformed: %w", err)
	}
	return manifestDigest, nil
}

func rustSDKDescriptorDigest() (digest.Digest, error) {
	descriptorDigest := digest.Digest(os.Getenv(distconsts.RustSDKDescriptorDigestEnvName))
	if err := descriptorDigest.Validate(); err != nil {
		return "", fmt.Errorf("rust SDK provenance: packaged descriptor digest is absent or malformed: %w", err)
	}
	return descriptorDigest, nil
}

// IsBuiltinSDKName reports whether source names a built-in SDK/runtime bundled
// in the engine (e.g. "go", "python", "dang"), optionally with an "@version"
// suffix — as opposed to an external module ref or local path. Such names are
// resolved in-engine when a module's runtime loads; they are not standalone
// modules that can be loaded from a path or ref.
func IsBuiltinSDKName(source string) bool {
	name, _, _ := strings.Cut(source, "@")
	return sdkmeta.IsBuiltin(name)
}
