// Command engine-image-entries exports the cache entries an engine image
// ships for Go module runtimes, on an engine of the image's own build: the
// builds of the standard library and of what every Go module's generated
// code imports, as dagger/go builds them for the built-in Go SDK, and the
// binary of dagger/go's gobuild, which the engine merges into its cache at
// start.
//
// It loads dagger/go from the Go SDK image, whose manifest digest is
// -go-sdk-digest, as the engine loads it for the Go SDK, so that every
// result is the one the engine's own builds look up. It takes the builds
// codegen's archives of the Go module in -seed read (archiveBuilds of its
// root package, its dependencies alone, without tests), with the Go SDK's
// -go-version and build flags, and gobuild's binary (Module._prebuild), and
// exports them with their blobs into -out.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"dagger.io/dagger"
)

// goSDKBuildFlags are the build flags the Go SDK builds every module runtime
// with (sdk/go/runtime/main.dang).
var goSDKBuildFlags = []string{"-ldflags=-s -w"}

func main() {
	seed := flag.String("seed", "", "the Go module whose dependencies' builds to export")
	goSDKDigest := flag.String("go-sdk-digest", "", "the manifest digest of the engine's Go SDK image")
	goVersion := flag.String("go-version", "", "the Go version the Go SDK builds with")
	out := flag.String("out", "", "where to export the entries")
	flag.Parse()
	if *seed == "" || *goSDKDigest == "" || *goVersion == "" || *out == "" {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(context.Background(), *seed, *goSDKDigest, *goVersion, *out); err != nil {
		fmt.Fprintln(os.Stderr, "engine-image-entries:", err)
		os.Exit(1)
	}
}

// goSDKDaggerGo selects dagger/go in the Go SDK image, as the Go SDK module
// (sdk/go/runtime) depends on it there: the image's /sdk/dagger-go.
const goSDKDaggerGo = `_builtinContainer(digest: $digest) { rootfs { directory(path: "/sdk") { asModuleSource(sourceRootPath: "dagger-go") { asModule { %s } } } } }`

func run(ctx context.Context, seed, goSDKDigest, goVersion, out string) error {
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer client.Close()
	do := func(query string, vars map[string]any, data any) error {
		return client.Do(ctx, &dagger.Request{Query: query, Variables: vars}, &dagger.Response{Data: data})
	}

	digest := map[string]any{"digest": goSDKDigest}
	if err := do(`query($digest: String!) { `+fmt.Sprintf(goSDKDaggerGo, "serve")+` }`, digest, nil); err != nil {
		return fmt.Errorf("load dagger/go: %w", err)
	}
	// gobuild's binary: dagger/go's dependency gobuild declares it as its
	// entrypoint's prebuild member.
	var deps struct {
		BuiltinContainer struct {
			Rootfs struct {
				Directory struct {
					AsModuleSource struct {
						AsModule struct {
							Dependencies []struct {
								Name     string `json:"name"`
								Prebuild *struct {
									ID string `json:"id"`
								} `json:"_prebuild"`
							} `json:"dependencies"`
						} `json:"asModule"`
					} `json:"asModuleSource"`
				} `json:"directory"`
			} `json:"rootfs"`
		} `json:"_builtinContainer"`
	}
	if err := do(`query($digest: String!) { `+fmt.Sprintf(goSDKDaggerGo, "dependencies { name _prebuild { id } }")+` }`, digest, &deps); err != nil {
		return fmt.Errorf("gobuild's binary: %w", err)
	}
	var files []string
	for _, dep := range deps.BuiltinContainer.Rootfs.Directory.AsModuleSource.AsModule.Dependencies {
		if dep.Name == "gobuild" && dep.Prebuild != nil {
			files = append(files, dep.Prebuild.ID)
		}
	}
	if len(files) != 1 {
		return fmt.Errorf("gobuild's binary: dagger/go's dependency gobuild declares no prebuild member")
	}
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
		return fmt.Errorf("seed workspace: %w", err)
	}
	var builds struct {
		Go struct {
			Module struct {
				ArchiveBuilds []string `json:"archiveBuilds"`
			} `json:"module"`
		} `json:"go"`
	}
	if err := do(`query($ws: ID!, $version: String!, $flags: [String!]!) {
		go(version: $version, buildFlags: $flags) {
			module(ws: $ws, path: ".", findUp: false) {
				archiveBuilds(ws: $ws, dependencies: true, packages: ["."], tests: false)
			}
		}
	}`, map[string]any{"ws": ws.Host.Directory.AsWorkspace.ID, "version": goVersion, "flags": goSDKBuildFlags}, &builds); err != nil {
		return fmt.Errorf("archive builds: %w", err)
	}
	ids := builds.Go.Module.ArchiveBuilds
	if len(ids) == 0 {
		return errors.New("archive builds: none")
	}
	if err := do(`query($ids: [ID!]!, $files: [ID!]!, $out: String!) { _engineImageBundle(directories: $ids, files: $files) { export(path: $out) } }`,
		map[string]any{"ids": ids, "files": files, "out": out}, nil); err != nil {
		return fmt.Errorf("export the entries: %w", err)
	}
	fmt.Fprintf(os.Stderr, "engine-image-entries: exported the builds of %d packages and gobuild's binary to %s\n", len(ids), out)
	return nil
}
