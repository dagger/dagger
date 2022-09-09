package project

import (
	"context"
	"embed"
	"io/fs"
	"path/filepath"
	"sort"

	"github.com/dagger/cloak/core/filesystem"
	"github.com/moby/buildkit/client/llb"
)

//go:embed go/*
var goGenerateSrc embed.FS

func (s RemoteSchema) goGenerate(ctx context.Context, subpath, schema, coreSchema, coreOperations string) (*filesystem.Filesystem, error) {
	projectFS, err := s.contextFS.ToState()
	if err != nil {
		return nil, err
	}

	base := goBase(s.gw)

	// Setup the generate tool in its own directory.
	// gqlgen and genqlient need their own go module to execute, but we don't want to use
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
			`go mod init cloakgenerate`,
			`go work init`,
			`go work use .`,
			`go work use -r /src`,
			`go build -o generate generate.go`,
		),
		llb.Dir("/tools"),
		llb.AddMount("/src", projectFS, llb.Readonly),
		llb.AddEnv("CGO_ENABLED", "0"),
		withGoCaching(),
		withGoPrivateRepoConfiguration(s.sshAuthSockID),
	).Root()

	// setup core schemas+operations for client codegen
	projectFS = withClientMetadata(projectFS, subpath, s.configPath, "core", coreSchema, coreOperations)

	// setup dependency schemas+operations for client codegen
	for _, dep := range s.dependencies {
		depSchema := dep.Schema() + "\n" + coreSchema
		projectFS = withClientMetadata(projectFS, subpath, s.configPath, dep.Name(), depSchema, dep.Operations())
	}

	// setup schemas+operations of extensions in this project for client codegen
	var selfSchema string
	var selfOperations string
	for _, otherExt := range s.extensions {
		selfSchema += otherExt.Schema + "\n"
		selfOperations += otherExt.Operations + "\n"
	}
	if selfOperations != "" {
		selfSchema += coreSchema
		projectFS = withClientMetadata(projectFS, subpath, s.configPath, s.Name(), selfSchema, selfOperations)
	}

	// generate client stubs and extension/script skeletons
	projectSubpath := filepath.Join(filepath.Dir(s.configPath), subpath)
	outputDir := filepath.Join("/src", projectSubpath)
	projectFS = base.Run(
		llb.Shlex("./generate"),
		llb.Dir("/tools"),
		// mount only the project subdirectory that should receive changes as read-write, the rest is ro
		llb.AddMount("/src", projectFS, llb.Readonly),
		llb.AddMount(outputDir, projectFS, llb.SourcePath(projectSubpath)),
		llb.AddEnv("CGO_ENABLED", "0"),
		llb.AddEnv("GENERATE_OUTPUT_DIR", outputDir),
		llb.AddEnv("SCHEMA", schema),
		llb.AddEnv("CORE_SCHEMA", coreSchema),
		withGoCaching(),
		withGoPrivateRepoConfiguration(s.sshAuthSockID),
	).GetMount(outputDir)

	return filesystem.FromState(ctx, projectFS, s.platform)
}

// Create the schema.graphql and operations.graphql files needed for client codegen.
// The actual code generation is done by the generate tool.
func withClientMetadata(projectFS llb.State, subpath, configPath, name, schema, operations string) llb.State {
	outputDir := filepath.Join(filepath.Dir(configPath), subpath, "gen", name)
	return projectFS.
		File(llb.Mkdir(outputDir, 0755, llb.WithParents(true))).
		File(llb.Mkfile(filepath.Join(outputDir, ".gitattributes"), 0644, []byte(`** linguist-generated=true`))).
		File(llb.Mkfile(filepath.Join(outputDir, "schema.graphql"), 0644, []byte(schema))).
		File(llb.Mkfile(filepath.Join(outputDir, "operations.graphql"), 0644, []byte(operations)))
}
