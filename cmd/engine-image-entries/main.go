// Command engine-image-entries exports the cache entries an engine image
// ships for Go module runtimes, on an engine of the image's own build: the
// builds of the standard library and of what every Go module's generated
// code imports, as dagger/go builds them, with the module downloads and the
// tools those builds read, dagger/go's gobuild binary, and what a fresh
// engine would otherwise fetch to build with them: dagger/go's checkout, the
// golang image the builds run in and the image gobuild runs in. The engine
// merges them into its cache at start.
//
// It loads dagger/go from -dagger-go, the pin the Go SDK module and go-sdk's
// generated modules depend on, so that every result is the one their builds
// look up. It takes what codegen's archives of each Go module in -seed read
// from the cache (archiveBuilds of its root package, its dependencies alone,
// without tests), at -go-version with the Go SDK's build flags, or for an
// -entrypoint-seed with go-sdk's entrypoints', gobuild's binary
// (Module._prebuild), and exports them with their blobs into -out.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"

	"dagger.io/dagger"
)

// goSDKBuildFlags are the build flags the Go SDK builds every module runtime
// with (sdk/go/runtime/main.dang).
var goSDKBuildFlags = []string{"-ldflags=-s -w", "-gcflags=all=-dwarf=false", "-gcflags=std=", "-gcflags=./internal/dagger/...=-dwarf=false -l"}

// entrypointBuildFlags are the build flags go-sdk's generated entrypoints
// build with: none.
var entrypointBuildFlags []string

// seedFlags are the -seed flags, in order.
type seedFlags []string

func (s *seedFlags) String() string     { return strings.Join(*s, ",") }
func (s *seedFlags) Set(v string) error { *s = append(*s, v); return nil }

func main() {
	var seeds seedFlags
	var entrypointSeeds seedFlags
	flag.Var(&seeds, "seed", "a Go module whose dependencies' builds to export, with the Go SDK's flags (repeatable)")
	flag.Var(&entrypointSeeds, "entrypoint-seed", "a Go module whose dependencies' builds to export, with go-sdk's entrypoints' flags (repeatable)")
	daggerGo := flag.String("dagger-go", "", "the dagger/go module ref the Go SDK depends on")
	goVersion := flag.String("go-version", "", "the Go version the Go SDK builds with")
	out := flag.String("out", "", "where to export the entries")
	flag.Parse()
	if len(seeds) == 0 || *daggerGo == "" || *goVersion == "" || *out == "" {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(context.Background(), seeds, entrypointSeeds, *daggerGo, *goVersion, *out); err != nil {
		fmt.Fprintln(os.Stderr, "engine-image-entries:", err)
		os.Exit(1)
	}
}

// daggerGoModule selects dagger/go's module source by its pin.
const daggerGoModule = `moduleSource(refString: $pin) { %s }`

// fromImage is the image a call's container starts from in a generated
// entrypoint: the last .from("…") of the file.
var fromImage = regexp.MustCompile(`\.from\("([^"]+)"\)`)

func run(ctx context.Context, seeds, entrypointSeeds []string, daggerGo, goVersion, out string) error {
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer client.Close()
	do := func(query string, vars map[string]any, data any) error {
		return client.Do(ctx, &dagger.Request{Query: query, Variables: vars}, &dagger.Response{Data: data})
	}

	pin := map[string]any{"pin": daggerGo}
	if err := do(`query($pin: String!) { `+fmt.Sprintf(daggerGoModule, "asModule { serve }")+` }`, pin, nil); err != nil {
		return fmt.Errorf("load dagger/go: %w", err)
	}
	// dagger/go's module source, whose checkout the module loads from, and
	// its context directory; gobuild's binary, which
	// dagger/go's dependency gobuild declares as its entrypoint's prebuild
	// member; and the image gobuild's calls run in, from its entrypoint.
	var mod struct {
		ModuleSource struct {
			ID               string `json:"id"`
			ContextDirectory struct {
				ID   string `json:"id"`
				File struct {
					Contents string `json:"contents"`
				} `json:"file"`
			} `json:"contextDirectory"`
			AsModule struct {
				Dependencies []struct {
					Name     string `json:"name"`
					Prebuild *struct {
						ID string `json:"id"`
					} `json:"_prebuild"`
				} `json:"dependencies"`
			} `json:"asModule"`
		} `json:"moduleSource"`
	}
	if err := do(`query($pin: String!) { `+fmt.Sprintf(daggerGoModule, `id contextDirectory { id file(path: "gobuild/internal/dagger/entrypoint/main.dang") { contents } } asModule { dependencies { name _prebuild { id } } }`)+` }`, pin, &mod); err != nil {
		return fmt.Errorf("dagger/go: %w", err)
	}
	var files []string
	for _, dep := range mod.ModuleSource.AsModule.Dependencies {
		if dep.Name == "gobuild" && dep.Prebuild != nil {
			files = append(files, dep.Prebuild.ID)
		}
	}
	if len(files) != 1 {
		return fmt.Errorf("gobuild's binary: dagger/go's dependency gobuild declares no prebuild member")
	}
	images := fromImage.FindAllStringSubmatch(mod.ModuleSource.ContextDirectory.File.Contents, -1)
	if len(images) == 0 {
		return errors.New("gobuild's entrypoint names no image")
	}
	var goImage struct {
		Go struct {
			Image string `json:"image"`
		} `json:"go"`
	}
	if err := do(`query($version: String!) { go(version: $version) { image(version: $version) } }`, map[string]any{"version": goVersion}, &goImage); err != nil {
		return fmt.Errorf("the golang image: %w", err)
	}
	var containers []string
	for _, ref := range []string{goImage.Go.Image, images[len(images)-1][1]} {
		var ctr struct {
			Container struct {
				From struct {
					ID string `json:"id"`
				} `json:"from"`
			} `json:"container"`
		}
		if err := do(`query($ref: String!) { container { from(address: $ref) { id } } }`, map[string]any{"ref": ref}, &ctr); err != nil {
			return fmt.Errorf("image %s: %w", ref, err)
		}
		containers = append(containers, ctr.Container.From.ID)
	}
	// The seeds share what they import alike, such as the standard library,
	// and the tools: each result once.
	var ids []string
	seen := map[string]bool{}
	exportSeeds := func(seeds []string, flags []string) error {
		for _, seed := range seeds {
			seedIDs, err := archiveBuilds(do, seed, goVersion, flags)
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
	if err := exportSeeds(seeds, goSDKBuildFlags); err != nil {
		return err
	}
	if err := exportSeeds(entrypointSeeds, entrypointBuildFlags); err != nil {
		return err
	}
	results := append(append(append(ids, mod.ModuleSource.ID, mod.ModuleSource.ContextDirectory.ID), files...), containers...)
	if err := do(`query($results: [ID!]!, $out: String!) { _engineImageBundle(results: $results) { export(path: $out) } }`,
		map[string]any{"results": results, "out": out}, nil); err != nil {
		return fmt.Errorf("export the entries: %w", err)
	}
	fmt.Fprintf(os.Stderr, "engine-image-entries: exported %d results dagger/go's archive builds read (package builds, module downloads and tools), gobuild's binary, dagger/go's checkout and context, and the images %s and %s to %s\n",
		len(ids), goImage.Go.Image, images[len(images)-1][1], out)
	return nil
}

// archiveBuilds is what codegen's archives of the Go module in seed read
// from the cache, as IDs: archiveBuilds of its root package, its
// dependencies alone, without tests, at goVersion with flags.
func archiveBuilds(do func(string, map[string]any, any) error, seed, goVersion string, flags []string) ([]string, error) {
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
				ArchiveBuilds []string `json:"archiveBuilds"`
			} `json:"module"`
		} `json:"go"`
	}
	if err := do(`query($ws: ID!, $version: String!, $flags: [String!]!) {
		go(version: $version, buildFlags: $flags) {
			module(ws: $ws, path: ".", findUp: false) {
				archiveBuilds(ws: $ws, dependencies: true, packages: ["."], noTests: true)
			}
		}
	}`, map[string]any{"ws": ws.Host.Directory.AsWorkspace.ID, "version": goVersion, "flags": flags}, &builds); err != nil {
		return nil, fmt.Errorf("archive builds: %w", err)
	}
	if len(builds.Go.Module.ArchiveBuilds) == 0 {
		return nil, errors.New("archive builds: none")
	}
	return builds.Go.Module.ArchiveBuilds, nil
}
