// Command engine-image-entries exports the cache entries an engine image
// ships for Go module runtimes, on an engine of the image's own build: the
// builds of the standard library and of what every Go module's generated
// code imports, as dagger/go builds them for the built-in Go SDK, which the
// engine merges into its cache at start.
//
// It loads dagger/go from -dagger-go, takes the builds codegen's archives of
// the Go module in -seed read (archiveBuilds of its root package, its
// dependencies alone, without tests), with the Go SDK's -go-version and
// build flags, and exports them with their blobs into -out.
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
	daggerGo := flag.String("dagger-go", "", "the dagger/go module the Go SDK builds with")
	goVersion := flag.String("go-version", "", "the Go version the Go SDK builds with")
	out := flag.String("out", "", "where to export the entries")
	flag.Parse()
	if *seed == "" || *daggerGo == "" || *goVersion == "" || *out == "" {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(context.Background(), *seed, *daggerGo, *goVersion, *out); err != nil {
		fmt.Fprintln(os.Stderr, "engine-image-entries:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, seed, daggerGo, goVersion, out string) error {
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer client.Close()
	do := func(query string, vars map[string]any, data any) error {
		return client.Do(ctx, &dagger.Request{Query: query, Variables: vars}, &dagger.Response{Data: data})
	}

	if err := do(`query($src: String!) { moduleSource(refString: $src) { asModule { serve } } }`,
		map[string]any{"src": daggerGo}, nil); err != nil {
		return fmt.Errorf("load dagger/go: %w", err)
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
	if err := do(`query($ids: [ID!]!, $out: String!) { _engineImageBundle(directories: $ids) { export(path: $out) } }`,
		map[string]any{"ids": ids, "out": out}, nil); err != nil {
		return fmt.Errorf("export the entries: %w", err)
	}
	fmt.Fprintf(os.Stderr, "engine-image-entries: exported the builds of %d packages to %s\n", len(ids), out)
	return nil
}
