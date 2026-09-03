package schema

import (
	"context"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
)

type moduleManifestSchema struct{}

var _ SchemaResolvers = &moduleManifestSchema{}

func (s *moduleManifestSchema) Install(dag *dagql.Server) {
	dagql.Fields[*core.Query]{
		dagql.Func("moduleManifest", s.moduleManifest).
			View(AfterVersion("v1.0.0-0")).
			Doc("Construct a versioned module manifest."),
	}.Install(dag)

	dagql.Fields[*core.ModuleManifestVersions]{
		dagql.Func("v1", s.v1).
			Doc("Construct a manifest in the current unversioned format.",
				"The generated file does not contain manifestVersion = 1.").
			Args(dagql.Arg("name").Doc("Module name.")),
		dagql.Func("v2", s.v2).
			Doc("Construct a manifest in format version 2.").
			Args(dagql.Arg("name").Doc("Module name.")),
	}.Install(dag)

	dagql.Fields[*core.ModuleManifestV1]{
		dagql.Func("withRuntime", s.withRuntime).
			Doc("Set the runtime that builds and executes the module.").
			Args(dagql.Arg("source").Doc("Runtime source.")),
		dagql.Func("withSource", s.withSource).
			Doc("Set the module implementation path relative to dagger-module.toml.",
				"The default is '.'.").
			Args(dagql.Arg("path").Doc("Module implementation path.")),
		dagql.Func("withEngineVersion", s.withEngineVersion).
			Doc("Set the required engine API version.",
				"The default is the running engine version.").
			Args(dagql.Arg("version").Doc("Required engine API version.")),
		dagql.Func("withInclude", s.withInclude).
			Doc("Add a path from the module context to the runtime input.",
				"This operation is additive.").
			Args(dagql.Arg("path").Doc("Path to include.")),
		dagql.Func("asFile", s.v1AsFile).
			Doc("Serialize the manifest as dagger-module.toml."),
	}.Install(dag)

	dagql.Fields[*core.ModuleManifestV2]{
		dagql.Func("withDangEntrypoint", s.withDangEntrypoint).
			Doc("Use the built-in Dang entrypoint driver.").
			Args(dagql.Arg("source").Doc("Entrypoint source address.")),
		dagql.Func("withModuleEntrypoint", s.withModuleEntrypoint).
			Doc("Use another module as the entrypoint driver.").
			Args(dagql.Arg("source").Doc("Entrypoint module address.")),
		dagql.Func("asFile", s.v2AsFile).
			Doc("Serialize the manifest as dagger-module.toml."),
	}.Install(dag)
}

func (s *moduleManifestSchema) moduleManifest(
	ctx context.Context,
	query *core.Query,
	args struct{},
) (*core.ModuleManifestVersions, error) {
	return &core.ModuleManifestVersions{}, nil
}

func (s *moduleManifestSchema) v1(
	ctx context.Context,
	versions *core.ModuleManifestVersions,
	args struct{ Name string },
) (*core.ModuleManifestV1, error) {
	return &core.ModuleManifestV1{
		Name:          args.Name,
		EngineVersion: engine.NormalizeVersion(engine.Version),
		Source:        ".",
	}, nil
}

func (s *moduleManifestSchema) v2(
	ctx context.Context,
	versions *core.ModuleManifestVersions,
	args struct{ Name string },
) (*core.ModuleManifestV2, error) {
	return &core.ModuleManifestV2{Name: args.Name}, nil
}

func (s *moduleManifestSchema) withRuntime(
	ctx context.Context,
	manifest *core.ModuleManifestV1,
	args struct{ Source string },
) (*core.ModuleManifestV1, error) {
	return manifest.WithRuntime(args.Source), nil
}

func (s *moduleManifestSchema) withSource(
	ctx context.Context,
	manifest *core.ModuleManifestV1,
	args struct{ Path string },
) (*core.ModuleManifestV1, error) {
	return manifest.WithSource(args.Path), nil
}

func (s *moduleManifestSchema) withEngineVersion(
	ctx context.Context,
	manifest *core.ModuleManifestV1,
	args struct{ Version string },
) (*core.ModuleManifestV1, error) {
	version := args.Version
	if version == "" || version == modules.EngineVersionLatest || version == "current" {
		version = engine.Version
	}
	return manifest.WithEngineVersion(engine.NormalizeVersion(version)), nil
}

func (s *moduleManifestSchema) withInclude(
	ctx context.Context,
	manifest *core.ModuleManifestV1,
	args struct{ Path string },
) (*core.ModuleManifestV1, error) {
	return manifest.WithInclude(args.Path), nil
}

func (s *moduleManifestSchema) v1AsFile(
	ctx context.Context,
	manifest *core.ModuleManifestV1,
	args struct{},
) (dagql.ObjectResult[*core.File], error) {
	return manifest.AsFile(ctx)
}

func (s *moduleManifestSchema) withDangEntrypoint(
	ctx context.Context,
	manifest *core.ModuleManifestV2,
	args struct{ Source string },
) (*core.ModuleManifestV2, error) {
	return manifest.WithDangEntrypoint(args.Source), nil
}

func (s *moduleManifestSchema) withModuleEntrypoint(
	ctx context.Context,
	manifest *core.ModuleManifestV2,
	args struct{ Source string },
) (*core.ModuleManifestV2, error) {
	return manifest.WithModuleEntrypoint(args.Source), nil
}

func (s *moduleManifestSchema) v2AsFile(
	ctx context.Context,
	manifest *core.ModuleManifestV2,
	args struct{},
) (dagql.ObjectResult[*core.File], error) {
	return manifest.AsFile(ctx)
}
