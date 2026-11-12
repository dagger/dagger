package sdk

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/dagger/engine/engineutil"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	// goSDKDefaultGoProxy is the GOPROXY of Go module downloads when the
	// engine sets none (_DAGGER_ENGINE_SYSTEMENV_GOPROXY).
	goSDKDefaultGoProxy = "https://proxy.golang.org|https://proxy.golang.org|direct"

	// goSDKLibVersion is the dagger.io/dagger version module codegen pins in
	// generated code: v1.0.0-beta.12. The engine image's module cache is
	// seeded for it (engine-dev's goModuleSeed reads it from this file).
	goSDKLibVersion = "f70383e0aa389216628d304482664359300c9be1"
)

// loadBuiltinGoSDK loads the Go SDK the engine image carries: a Dang module
// SDK at sdk/runtime in the Go SDK's files (goSDKFiles).
func (l *Loader) loadBuiltinGoSDK(
	ctx context.Context,
	root *core.Query,
	sdk *core.SDKConfig,
) (*module, error) {
	dag, err := root.Server.Server(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag for sdk %s: %w", sdk.Source, err)
	}
	var sdkMod dagql.ObjectResult[*core.Module]
	if err := dag.Select(ctx, dag.Root(), &sdkMod,
		goSDKFiles(),
		dagql.Selector{Field: "directory", Args: []dagql.NamedInput{{Name: "path", Value: dagql.String("sdk")}}},
		dagql.Selector{Field: "asModuleSource", Args: []dagql.NamedInput{
			{Name: "sourceRootPath", Value: dagql.String("runtime")},
		}},
		dagql.Selector{Field: "asModule", Args: []dagql.NamedInput{
			{Name: "forceDefaultFunctionCaching", Value: dagql.Opt(dagql.Boolean(true))},
		}},
	); err != nil {
		return nil, fmt.Errorf("failed to load the go sdk module from the engine image: %w", err)
	}
	return newModuleSDKWith(ctx, root, sdkMod, dagql.ObjectResult[*core.Directory]{}, sdk.Config, moduleSDKOpts{
		builtinArgs: func(ctx context.Context) ([]dagql.NamedInput, error) {
			return goSDKConstructorArgs(ctx, root, sdk.Config)
		},
		// Generated bindings unconditionally include the module's own types,
		// so engine-side checks gated on SelfCallsEnabled (tolerating module
		// types that shadow core or dependency types) must match. The module
		// is NOT added to its own deps for codegen: the generator merges its
		// own types in a single pass, and an engine-side self-append would
		// collide with that merge.
		selfCalls:     true,
		debugTerminal: true,
		moduleTypes:   goModuleTypes{},
		generatedFiles: []string{
			"dagger.gen.go",
			filepath.Join("internal", "dagger", "dagger.gen.go"),
		},
	})
}

// goSDKContainer is the Go SDK's container: the Go toolchain image dagger/go
// builds in (distconsts.GoSDKGolangImage), so the two share it, with git and
// SSH added and the Go SDK's files mounted. The image's entries hold the
// image and the files; what the engine makes of them here is cheap, so the
// container's own calls need no entry: a call's API view is part of its key,
// and the engine that made the entries can give its client another view.
func goSDKContainer(ctx context.Context, dag *dagql.Server) (dagql.ObjectResult[*core.Container], error) {
	// These calls run in the calling client's session, which shows a call
	// once: unhidden, they would take the line of a module's own identical
	// call, such as its container().
	ctx = dagql.WithSkip(ctx)
	var base dagql.ObjectResult[*core.Container]
	var files dagql.ObjectResult[*core.Directory]
	if err := dag.Select(ctx, dag.Root(), &files, goSDKFiles()); err != nil {
		return base, fmt.Errorf("go sdk files: %w", err)
	}
	dirID := func(path string) (*call.ID, error) {
		var dir dagql.ObjectResult[*core.Directory]
		if err := dag.Select(ctx, files, &dir, dagql.Selector{Field: "directory", Args: []dagql.NamedInput{{Name: "path", Value: dagql.String(path)}}}); err != nil {
			return nil, fmt.Errorf("go sdk files' %s: %w", path, err)
		}
		return dir.ID()
	}
	fileID := func(path string) (*call.ID, error) {
		var file dagql.ObjectResult[*core.File]
		if err := dag.Select(ctx, files, &file, dagql.Selector{Field: "file", Args: []dagql.NamedInput{{Name: "path", Value: dagql.String(path)}}}); err != nil {
			return nil, fmt.Errorf("go sdk files' %s: %w", path, err)
		}
		return file.ID()
	}
	apk, err := dirID("apk")
	if err != nil {
		return base, err
	}
	sdk, err := dirID("sdk")
	if err != nil {
		return base, err
	}
	sels := []dagql.Selector{
		{Field: "container"},
		{Field: "from", Args: []dagql.NamedInput{{Name: "address", Value: dagql.String(distconsts.GoSDKGolangImage)}}},
		// What installing git and SSH added to the image: a few hundred
		// files, copied.
		{Field: "withDirectory", Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.String("/")},
			{Name: "source", Value: dagql.NewID[*core.Directory](apk)},
		}},
		{Field: "withEnvVariable", Args: []dagql.NamedInput{
			{Name: "name", Value: dagql.String("GOTOOLCHAIN")},
			{Name: "value", Value: dagql.String("auto")},
		}},
		{Field: "withMountedDirectory", Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.String("/sdk")},
			{Name: "source", Value: dagql.NewID[*core.Directory](sdk)},
		}},
	}
	for _, bin := range []string{"codegen", "export-driver"} {
		id, err := fileID("usr/local/bin/" + bin)
		if err != nil {
			return base, err
		}
		sels = append(sels, dagql.Selector{Field: "withMountedFile", Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.String("/usr/local/bin/" + bin)},
			{Name: "source", Value: dagql.NewID[*core.File](id)},
		}})
	}
	if err := dag.Select(ctx, dag.Root(), &base, sels...); err != nil {
		return base, fmt.Errorf("go sdk container: %w", err)
	}
	return base, nil
}

// goSDKFiles selects the Go SDK's files, a directory the engine image's
// entries hold, by the content digest its variable gives: the codegen binary
// and the export driver under usr/local/bin, the SDK's Dang module under
// sdk/runtime, the module cache codegen starts from under go/pkg/mod, and
// under apk what installing git and SSH adds to the Go toolchain image.
func goSDKFiles() dagql.Selector {
	return dagql.Selector{Field: "__bundledDirectory", Args: []dagql.NamedInput{
		{Name: "digest", Value: dagql.String(os.Getenv(distconsts.GoSDKDigestEnvName))},
	}}
}

// goSDKConstructorArgs are what the Go SDK module gets of the engine and the
// session, which a module can't reach itself: the Go SDK's container with the
// engine's GOPROXY and GODEBUG and the module cache codegen starts from, the
// engine's Go version and the build flags its image's entries are built
// with, the dagger.io/dagger version codegen pins, and,
// for a module with private dependencies, what the calling client fetches
// them with.
func goSDKConstructorArgs(ctx context.Context, root *core.Query, config map[string]any) ([]dagql.NamedInput, error) {
	dag, err := root.Server.Server(ctx)
	if err != nil {
		return nil, err
	}
	base, err := goSDKContainer(ctx, dag)
	if err != nil {
		return nil, err
	}
	var modCacheBase dagql.ObjectResult[*core.Directory]
	if err := dag.Select(ctx, dag.Root(), &modCacheBase, goSDKFiles(),
		dagql.Selector{Field: "directory", Args: []dagql.NamedInput{{Name: "path", Value: dagql.String("go/pkg/mod")}}},
	); err != nil {
		return nil, fmt.Errorf("go sdk files' module cache: %w", err)
	}
	var modCache dagql.ObjectResult[*core.CacheVolume]
	if err := dag.Select(ctx, dag.Root(), &modCache,
		dagql.Selector{Field: "cacheVolume", Args: []dagql.NamedInput{
			{Name: "key", Value: dagql.String("gomod")},
			{Name: "namespace", Value: dagql.String("internal")},
		}},
	); err != nil {
		return nil, fmt.Errorf("go sdk module cache: %w", err)
	}
	modCacheID, err := modCache.ID()
	if err != nil {
		return nil, err
	}
	modCacheBaseID, err := modCacheBase.ID()
	if err != nil {
		return nil, err
	}
	goProxy, ok := engineutil.SystemEnvValue("GOPROXY")
	if !ok {
		goProxy = goSDKDefaultGoProxy
	}
	if err := dag.Select(ctx, base, &base,
		dagql.Selector{Field: "withMountedCache", Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.String("/go/pkg/mod")},
			{Name: "cache", Value: dagql.NewID[*core.CacheVolume](modCacheID)},
			{Name: "sharing", Value: core.CacheSharingModeShared},
			{Name: "source", Value: dagql.Opt(dagql.NewID[*core.Directory](modCacheBaseID))},
		}},
		// GOPROXY is an ordinary variable: dagger/go downloads modules in the
		// golang image with the settings of the downloads container's
		// environment, which doesn't list system variables. It has the value
		// a system variable would, so an engine with another GOPROXY
		// downloads apart, under its own keys.
		dagql.Selector{Field: "withEnvVariable", Args: []dagql.NamedInput{
			{Name: "name", Value: dagql.String("GOPROXY")},
			{Name: "value", Value: dagql.String(goProxy)},
		}},
		dagql.Selector{Field: "__withSystemEnvVariable", Args: []dagql.NamedInput{
			{Name: "name", Value: dagql.String("GODEBUG")},
		}},
	); err != nil {
		return nil, fmt.Errorf("go sdk base container: %w", err)
	}
	baseID, err := base.ID()
	if err != nil {
		return nil, err
	}
	var buildFlags dagql.ArrayInput[dagql.String]
	for _, flag := range distconsts.GoSDKBuildFlags() {
		buildFlags = append(buildFlags, dagql.String(flag))
	}
	args := []dagql.NamedInput{
		{Name: "base", Value: dagql.NewID[*core.Container](baseID)},
		{Name: "goVersion", Value: dagql.String(distconsts.GolangVersion)},
		{Name: "buildFlags", Value: buildFlags},
		{Name: "libVersion", Value: dagql.String(goSDKLibVersion)},
	}

	// Only a module that names its private dependencies, with goprivate in
	// its sdk config as documented, gets what the client fetches them with.
	// These settings are part of the cache key of everything the SDK builds,
	// so leaving them out lets every client share any other module's
	// runtime, whatever its git identity or SSH agent.
	if goprivate, _ := config["goprivate"].(string); goprivate == "" {
		return args, nil
	}

	bk, err := root.Engine(ctx)
	if err != nil {
		return nil, err
	}
	gitconfig, err := bk.GetGitConfig(ctx)
	if err != nil {
		if code := status.Code(err); code != codes.PermissionDenied && code != codes.Unimplemented {
			return nil, err
		}
		// An in-engine SDK client has no host attachables by design, and a
		// client with a host policy can leave out the Git service. Its module
		// dependencies must still be buildable; they simply do not inherit the
		// caller's ambient Git configuration.
		gitconfig = nil
	}
	// Of the client's git configuration, a download uses only its URL
	// rewrites: its identity and push rewrites would only split the key.
	var entries dagql.ArrayInput[dagql.String]
	for _, entry := range gitconfig {
		key := strings.ToLower(entry.Key)
		if strings.HasPrefix(key, "url.") && strings.HasSuffix(key, ".insteadof") {
			entries = append(entries, dagql.String(entry.Key+"="+entry.Value))
		}
	}
	if len(entries) > 0 {
		args = append(args, dagql.NamedInput{Name: "gitConfig", Value: entries})
	}

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get client metadata from context: %w", err)
	}
	if clientMetadata.SSHAuthSocketPath != "" {
		var sock dagql.Result[*core.Socket]
		if err := dag.Select(ctx, dag.Root(), &sock,
			dagql.Selector{Field: "host"},
			dagql.Selector{Field: "_sshAuthSocket"},
		); err != nil {
			return nil, fmt.Errorf("failed to select internal socket: %w", err)
		}
		sockID, err := sock.ID()
		if err != nil {
			return nil, fmt.Errorf("failed to get ssh socket ID: %w", err)
		}
		args = append(args, dagql.NamedInput{Name: "sshAuthSocket", Value: dagql.Opt(dagql.NewID[*core.Socket](sockID))})
	}
	return args, nil
}
