package project

import (
	"context"
	"embed"
	"io/fs"
	"path/filepath"
	"sort"

	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"go.dagger.io/dagger/core"
)

//go:embed go/*
var goGenerateSrc embed.FS

func (p *State) goGenerate(ctx context.Context, subpath, schema, coreSchema string, gw bkgw.Client, platform specs.Platform) (*core.Directory, error) {
	// TODO(vito): handle platform?
	projectSt, rel, _, err := p.workdir.Decode()
	if err != nil {
		return nil, err
	}

	base := goBase(gw)

	// Setup the generate tool in its own directory.
	// gqlgen needs its own go module to execute, but we don't want to use
	// the user's go.mod from the project since we don't want to pollute it with tools that
	// only run here. However, those tools also need access to the user's go.mod so they can
	// resolve types. This is why we split the tools dir (at /tools) and the project dir (at
	// /src). We then connect them using a workspace so tools have access to both sets of
	// dependencies.
	//
	// Also, the generate tool is a separate standalone binary because we have a custom gqlgen
	// plugin. These plugins are still forced to use configuration where file paths are provided
	// and must exist, so we need to run the plugin entirely in a container. Eventually, this
	// should all be running in an extension, but there are a few more features needed before that
	// can happen (biggest being the ability to modify a mounted exec and then return it from
	// the extension). So instead, we just re-use the idea of the shim and embed the source code
	// so it can be built and exec'd here.
	entries, err := fs.ReadDir(goGenerateSrc, "go")
	if err != nil {
		return nil, err
	}
	base = base.File(llb.Mkdir("/tools", 0755))
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})
	for _, e := range entries {
		contents, err := fs.ReadFile(goGenerateSrc, filepath.Join("go", e.Name()))
		if err != nil {
			return nil, err
		}
		base = base.File(llb.Mkfile(
			filepath.Join("/tools", e.Name()),
			e.Type().Perm(),
			contents,
		))
	}
	base = base.Run(
		// It would have been nice to just have go.mod in the embedded files, but for some reason
		// embed will fail with an error if you try to embed a file from a different go module.
		// So instead we just init it here.
		shell(
			`go mod init daggergenerate`,
			`go get github.com/99designs/gqlgen`,
			`go work init`,
			`go work use .`,
			`go work use -r /src`,
			`go build -o generate generate.go`,
		),
		llb.Dir("/tools"),
		llb.AddMount("/src", projectSt, llb.Readonly, llb.SourcePath(rel)),
		llb.AddEnv("CGO_ENABLED", "0"),
		withGoCaching(),
	).Root()

	// generate extension/script skeletons
	projectSubpath := filepath.Join(filepath.Dir(p.configPath), subpath)
	outputDir := filepath.Join("/src", projectSubpath)

	projectMounts := withRunOpts(
		// mount only the project subdirectory that should receive changes as read-write, the rest is ro
		llb.AddMount("/src", projectSt, llb.Readonly),
		llb.AddMount(outputDir, projectSt, llb.SourcePath(projectSubpath)),
	)
	if projectSubpath == "." {
		// In the case where the subpath is also the root of the project, we only need one mount.
		// If we don't implement it this way, buildkit will actually return just the projectFS unchanged
		// (most likely because it gets the /src read-only mount and considers that a no-op?)
		projectMounts = llb.AddMount(outputDir, projectSt)
	}
	projectSt = base.Run(
		llb.Shlex("./generate"),
		llb.Dir("/tools"),
		projectMounts,
		llb.AddEnv("CGO_ENABLED", "0"),
		llb.AddEnv("GENERATE_OUTPUT_DIR", outputDir),
		llb.AddEnv("SCHEMA", schema),
		llb.AddEnv("CORE_SCHEMA", coreSchema),
		withGoCaching(),
	).GetMount(outputDir)

	return core.NewDirectory(ctx, projectSt, "", platform)
}
