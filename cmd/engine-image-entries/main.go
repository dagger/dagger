// Command engine-image-entries exports the cache entries an engine image
// ships for Go module runtimes, on an engine of the image's own build: the
// builds of the standard library and of what every Go module's generated
// code imports, as dagger/go builds them, with the module downloads and the
// tools those builds read, dagger/go's gobuild binary, and what a fresh
// engine would otherwise fetch to build with them: dagger/go's checkout, the
// golang image the builds run in and the image gobuild runs in. The engine
// merges them into its cache at start.
//
// It loads the built-in Go SDK module from -builtin-go-sdk, and dagger/go as
// that module's dependency go, and fails unless -entrypoint-dagger-go, the
// dagger/go go-sdk's generated modules depend on, names the same, so that
// every result is the one their builds look up.
// It takes what codegen's archives of each Go module in -seed read from the
// cache (archiveBuilds of its root package, its dependencies alone, without
// tests), at -go-version with the Go SDK's build flags
// (distconsts.GoSDKBuildFlags), or for an -entrypoint-seed with dagger/go's
// own, as go-sdk's entrypoints build; gobuild's binary and the image its
// calls run in, which its entrypoint declares (Module._prebuild and
// Module._runtimeImage); and exports them with their blobs into -out.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/distconsts"
)

// withID is a result's ID in this session.
type withID struct {
	ID string `json:"id"`
}

// seedFlags are the -seed flags, in order.
type seedFlags []string

func (s *seedFlags) String() string     { return strings.Join(*s, ",") }
func (s *seedFlags) Set(v string) error { *s = append(*s, v); return nil }

func main() {
	var seeds seedFlags
	var entrypointSeeds seedFlags
	flag.Var(&seeds, "seed", "a Go module whose dependencies' builds to export, with the Go SDK's flags (repeatable)")
	flag.Var(&entrypointSeeds, "entrypoint-seed", "a Go module whose dependencies' builds to export, with dagger/go's own flags, as go-sdk's entrypoints build (repeatable)")
	builtinGoSDK := flag.String("builtin-go-sdk", "", "the built-in Go SDK module's directory, sdk/go/runtime")
	entrypointDaggerGo := flag.String("entrypoint-dagger-go", "", "the dagger/go module ref go-sdk's entrypoints build with, which must be the built-in Go SDK's")
	goVersion := flag.String("go-version", "", "the Go version the Go SDK builds with")
	out := flag.String("out", "", "where to export the entries")
	platform := flag.String("platform", "", "the platform of the engine image the entries are for (default: the engine's)")
	flag.Parse()
	if len(seeds) == 0 || *builtinGoSDK == "" || *goVersion == "" || *out == "" {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(context.Background(), seeds, entrypointSeeds, *builtinGoSDK, *entrypointDaggerGo, *goVersion, *platform, *out); err != nil {
		fmt.Fprintln(os.Stderr, "engine-image-entries:", err)
		os.Exit(1)
	}
}

// builtinGoSDKModule selects the built-in Go SDK module's source from its
// directory.
const builtinGoSDKModule = `host { directory(path: $builtinGoSDK) { asModuleSource { %s } } }`

// daggerGoSource is what names a dagger/go module source: its repository,
// commit and module.
type daggerGoSource struct {
	CloneRef          string `json:"cloneRef"`
	Commit            string `json:"commit"`
	SourceRootSubpath string `json:"sourceRootSubpath"`
}

func run(ctx context.Context, seeds, entrypointSeeds []string, builtinGoSDK, entrypointDaggerGo, goVersion, platform, out string) error {
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer client.Close()
	do := func(query string, vars map[string]any, data any) error {
		return client.Do(ctx, &dagger.Request{Query: query, Variables: vars}, &dagger.Response{Data: data})
	}

	// dagger/go is the Go SDK module's dependency go, as the engine loads it
	// for the SDK, served with it.
	vars := map[string]any{"builtinGoSDK": builtinGoSDK}
	if err := do(`query($builtinGoSDK: String!) { `+fmt.Sprintf(builtinGoSDKModule, "asModule { serve(includeDependencies: true) }")+` }`, vars, nil); err != nil {
		return fmt.Errorf("load the Go SDK module: %w", err)
	}
	// dagger/go's module source, whose checkout the module loads from, and
	// its context directory; and gobuild's binary and the image its calls
	// run in, which dagger/go's dependency gobuild declares as its
	// entrypoint's prebuild and runtimeImage members, as an engine of the
	// image's platform builds and pulls them.
	var mod struct {
		Host struct {
			Directory struct {
				AsModuleSource struct {
					Dependencies []struct {
						daggerGoSource
						ModuleName       string `json:"moduleName"`
						ID               string `json:"id"`
						ContextDirectory withID `json:"contextDirectory"`
						AsModule         struct {
							Dependencies []struct {
								Name         string  `json:"name"`
								Prebuild     *withID `json:"_prebuild"`
								RuntimeImage *withID `json:"_runtimeImage"`
							} `json:"dependencies"`
						} `json:"asModule"`
					} `json:"dependencies"`
				} `json:"asModuleSource"`
			} `json:"directory"`
		} `json:"host"`
	}
	params, platformArg := "$builtinGoSDK: String!", ""
	if platform != "" {
		params, platformArg = params+", $platform: Platform", "(platform: $platform)"
		vars["platform"] = platform
	}
	if err := do(`query(`+params+`) { `+fmt.Sprintf(builtinGoSDKModule, `dependencies { moduleName id cloneRef commit sourceRootSubpath contextDirectory { id } asModule { dependencies { name _prebuild`+platformArg+` { id } _runtimeImage`+platformArg+` { id } } } }`)+` }`, vars, &mod); err != nil {
		return fmt.Errorf("dagger/go: %w", err)
	}
	var daggerGo []string
	var gobuild []string
	for _, dep := range mod.Host.Directory.AsModuleSource.Dependencies {
		if dep.ModuleName != "go" {
			continue
		}
		if err := samePin(do, dep.daggerGoSource, entrypointDaggerGo); err != nil {
			return err
		}
		daggerGo = append(daggerGo, dep.ID, dep.ContextDirectory.ID)
		for _, dep := range dep.AsModule.Dependencies {
			if dep.Name == "gobuild" && dep.Prebuild != nil && dep.RuntimeImage != nil {
				gobuild = append(gobuild, dep.Prebuild.ID, dep.RuntimeImage.ID)
			}
		}
	}
	if len(daggerGo) != 2 {
		return errors.New("dagger/go: the Go SDK module has no dependency go")
	}
	if len(gobuild) != 2 {
		return errors.New("gobuild: dagger/go's dependency gobuild declares no prebuild or runtimeImage member")
	}
	var goImage struct {
		Go struct {
			Image string `json:"image"`
		} `json:"go"`
	}
	if err := do(`query($version: String!) { go(version: $version) { image(version: $version) } }`, map[string]any{"version": goVersion}, &goImage); err != nil {
		return fmt.Errorf("the golang image: %w", err)
	}
	var golang struct {
		Container struct {
			From withID `json:"from"`
		} `json:"container"`
	}
	if err := do(`query($ref: String!, $platform: Platform) { container(platform: $platform) { from(address: $ref) { id } } }`, map[string]any{"ref": goImage.Go.Image, "platform": optional(platform)}, &golang); err != nil {
		return fmt.Errorf("image %s: %w", goImage.Go.Image, err)
	}
	// The seeds share what they import alike, such as the standard library,
	// and the tools: each result once.
	var ids []string
	seen := map[string]bool{}
	exportSeeds := func(seeds []string, flags, shared []string) error {
		for _, seed := range seeds {
			seedIDs, err := archiveBuilds(do, seed, goVersion, platform, flags, shared)
			if err != nil {
				return fmt.Errorf("seed %s: %w", seed, err)
			}
			for _, id := range seedIDs {
				if !seen[id] {
					seen[id] = true
					ids = append(ids, id)
				}
			}
		}
		return nil
	}
	// The Go SDK's builds share what generated code imports: each seed's
	// archive builds name the dependency calls for what those imports
	// reach, the same calls a module's runtime build makes.
	if err := exportSeeds(seeds, distconsts.GoSDKBuildFlags(), distconsts.GoSDKSharedImports()); err != nil {
		return err
	}
	// go-sdk's entrypoints build with dagger/go's own flags: they set none.
	if err := exportSeeds(entrypointSeeds, nil, nil); err != nil {
		return err
	}
	results := append(append(append(ids, daggerGo...), gobuild...), golang.Container.From.ID)
	// The blobs are uncompressed: the image's own layer compresses them for
	// download as well as compressed blobs would be, and an engine then
	// unpacks each with no decompression the first time it uses it.
	if err := do(`query($results: [ID!]!, $out: String!) { engine { localCache { _export(results: $results, forcedCompression: UNCOMPRESSED) { export(path: $out) } } } }`,
		map[string]any{"results": results, "out": out}, nil); err != nil {
		return fmt.Errorf("export the entries: %w", err)
	}
	fmt.Fprintf(os.Stderr, "engine-image-entries: exported %d results dagger/go's archive builds read (package builds, module downloads and tools), gobuild's binary and image, dagger/go's checkout and context, and the image %s to %s\n",
		len(ids), goImage.Go.Image, out)
	return nil
}

// samePin fails unless go-sdk's entrypoints build with the dagger/go the
// built-in Go SDK module depends on, builtin: the same repository, commit and
// module. One engine session serves one dagger/go, and its builds are the
// ones both seeds export, so a go-sdk module building with another would
// find none of them.
func samePin(do func(string, map[string]any, any) error, builtin daggerGoSource, entrypointDaggerGo string) error {
	var res struct {
		ModuleSource daggerGoSource `json:"moduleSource"`
	}
	if err := do(`query($pin: String!) { moduleSource(refString: $pin) { cloneRef commit sourceRootSubpath } }`, map[string]any{"pin": entrypointDaggerGo}, &res); err != nil {
		return fmt.Errorf("go-sdk's dagger/go %s: %w", entrypointDaggerGo, err)
	}
	if res.ModuleSource != builtin {
		return fmt.Errorf("go-sdk's entrypoints build with dagger/go %s (go-sdk's versions.toml daggerGo), and the built-in Go SDK module with %s@%s (sdk/go/runtime/dagger-module.toml): the image's builds serve both only when the two pins name the same dagger/go, so move them together", entrypointDaggerGo, builtin.CloneRef, builtin.Commit)
	}
	return nil
}

// archiveBuilds is what codegen's archives of the Go module in seed read
// from the cache, as this session's IDs of them: archiveBuilds of its root
// package, its dependencies alone, without tests, at goVersion with flags,
// or with dagger/go's own when flags is nil, and with shared as its shared
// imports: the dependency calls for what those reach, whole, as well as
// each package's build.
func archiveBuilds(do func(string, map[string]any, any) error, seed, goVersion, platform string, flags, shared []string) ([]string, error) {
	var ws struct {
		Host struct {
			Directory struct {
				AsWorkspace struct {
					ID string `json:"id"`
				} `json:"asWorkspace"`
			} `json:"directory"`
		} `json:"host"`
	}
	if err := do(`query($path: String!) { host { directory(path: $path) { asWorkspace { id } } } }`,
		map[string]any{"path": seed}, &ws); err != nil {
		return nil, fmt.Errorf("workspace: %w", err)
	}
	var builds struct {
		Go struct {
			Module struct {
				ArchiveBuilds struct {
					Builds         []withID `json:"builds"`
					Downloads      []withID `json:"downloads"`
					Tool           withID   `json:"tool"`
					ManifestTool   withID   `json:"manifestTool"`
					ManifestSource withID   `json:"manifestSource"`
					Tools          withID   `json:"tools"`
					HelperBinary   withID   `json:"helperBinary"`
					// The shared dependency calls' results, by ID.
					SharedDependencies []string `json:"sharedDependencies"`
				} `json:"archiveBuilds"`
			} `json:"module"`
		} `json:"go"`
	}
	vars := map[string]any{"ws": ws.Host.Directory.AsWorkspace.ID, "version": goVersion}
	flagsArg, flagsVar := "", ""
	if flags != nil {
		flagsArg, flagsVar = ", buildFlags: $flags", ", $flags: [String!]!"
		vars["flags"] = flags
	}
	if shared != nil {
		flagsArg, flagsVar = flagsArg+", sharedImports: $shared", flagsVar+", $shared: [String!]!"
		vars["shared"] = shared
	}
	platformArg, platformVar := "", ""
	if platform != "" {
		platformArg, platformVar = ", platform: $platform", ", $platform: String"
		vars["platform"] = platform
	}
	if err := do(`query($ws: ID!, $version: String!`+flagsVar+platformVar+`) {
		go(version: $version`+flagsArg+`) {
			module(ws: $ws, path: ".", findUp: false) {
				archiveBuilds(ws: $ws, dependencies: true, packages: ["."], noTests: true`+platformArg+`) {
					builds { id }
					downloads { id }
					tool { id }
					manifestTool { id }
					manifestSource { id }
					tools { id }
					helperBinary { id }
					sharedDependencies
				}
			}
		}
	}`, vars, &builds); err != nil {
		return nil, fmt.Errorf("archive builds: %w", err)
	}
	// Each result's ID is taken here, in this session: the archive builds
	// are results, and their IDs this session's handles to them.
	got := builds.Go.Module.ArchiveBuilds
	if len(got.Builds) == 0 {
		return nil, errors.New("archive builds: none")
	}
	ids := append([]string(nil), got.SharedDependencies...)
	for _, list := range [][]withID{got.Builds, got.Downloads, {got.Tool, got.ManifestTool, got.ManifestSource, got.Tools, got.HelperBinary}} {
		for _, r := range list {
			ids = append(ids, r.ID)
		}
	}
	return ids, nil
}

// optional is v as a nullable argument: null when empty.
func optional(v string) any {
	if v == "" {
		return nil
	}
	return v
}
