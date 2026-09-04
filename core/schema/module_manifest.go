package schema

import (
	"context"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

type moduleManifestSchema struct{}

var _ SchemaResolvers = &moduleManifestSchema{}

func (s *moduleManifestSchema) Install(dag *dagql.Server) {
	dagql.Fields[*core.Query]{
		dagql.Func("moduleManifest", s.moduleManifest).
			View(AfterVersion("v1.0.0-0")).
			Doc("Construct a module manifest.").
			Args(dagql.Arg("name").Doc("Module name.")),
	}.Install(dag)

	dagql.Fields[*core.ModuleManifest]{
		dagql.Func("withDangEntrypoint", s.withDangEntrypoint).
			Doc("Use the built-in Dang entrypoint.").
			Args(dagql.Arg("source").Doc("Entrypoint source address.")),
		dagql.Func("withModuleEntrypoint", s.withModuleEntrypoint).
			Doc("Use another module as the entrypoint.").
			Args(dagql.Arg("source").Doc("Entrypoint module address.")),
		dagql.Func("withLegacyGoRuntime", legacyRuntimeResolver((*core.ModuleManifest).WithLegacyGoRuntime)).
			Doc("Add the legacy Go runtime.").
			Args(legacyRuntimeArgs()...),
		dagql.Func("withLegacyDangRuntime", legacyRuntimeResolver((*core.ModuleManifest).WithLegacyDangRuntime)).
			Doc("Add the legacy Dang runtime.").
			Args(legacyRuntimeArgs()...),
		dagql.Func("withLegacyPythonRuntime", legacyRuntimeResolver((*core.ModuleManifest).WithLegacyPythonRuntime)).
			Doc("Add the legacy Python runtime.").
			Args(legacyRuntimeArgs()...),
		dagql.Func("withLegacyTypescriptRuntime", legacyRuntimeResolver((*core.ModuleManifest).WithLegacyTypescriptRuntime)).
			Doc("Add the legacy TypeScript runtime.").
			Args(legacyRuntimeArgs()...),
		dagql.Func("withLegacyPHPRuntime", legacyRuntimeResolver((*core.ModuleManifest).WithLegacyPHPRuntime)).
			Doc("Add the legacy PHP runtime.").
			Args(legacyRuntimeArgs()...),
		dagql.Func("withLegacyElixirRuntime", legacyRuntimeResolver((*core.ModuleManifest).WithLegacyElixirRuntime)).
			Doc("Add the legacy Elixir runtime.").
			Args(legacyRuntimeArgs()...),
		dagql.Func("withLegacyJavaRuntime", legacyRuntimeResolver((*core.ModuleManifest).WithLegacyJavaRuntime)).
			Doc("Add the legacy Java runtime.").
			Args(legacyRuntimeArgs()...),
		dagql.Func("withLegacyInclude", s.withLegacyInclude).
			Doc("Add an include path for the legacy runtime.",
				"This operation is additive.").
			Args(dagql.Arg("path").Doc("Path to include.")),
		dagql.Func("withoutLegacyFields", s.withoutLegacyFields).
			Doc("Remove the legacy runtime, module source, engine version, and include paths."),
		dagql.Func("validate", s.validate).
			Doc("Validate the manifest.",
				"If targetEngineVersion is set, also validate the legacy runtime against that engine version.").
			Args(dagql.Arg("targetEngineVersion").Doc("Optional target engine version.")),
		dagql.Func("tomlFile", s.tomlFile).
			Doc("Serialize the manifest as dagger-module.toml."),
		dagql.Func("legacyJSONFile", s.legacyJSONFile).
			Doc("Serialize the legacy fields as dagger.json."),
		dagql.Func("directory", s.directory).
			Doc("Return a directory with all applicable manifest files."),
	}.Install(dag)
}

func (s *moduleManifestSchema) moduleManifest(
	ctx context.Context,
	query *core.Query,
	args struct{ Name string },
) (*core.ModuleManifest, error) {
	return &core.ModuleManifest{Name: args.Name}, nil
}

func (s *moduleManifestSchema) withDangEntrypoint(
	ctx context.Context,
	manifest *core.ModuleManifest,
	args struct{ Source string },
) (*core.ModuleManifest, error) {
	return manifest.WithDangEntrypoint(args.Source), nil
}

func (s *moduleManifestSchema) withModuleEntrypoint(
	ctx context.Context,
	manifest *core.ModuleManifest,
	args struct{ Source string },
) (*core.ModuleManifest, error) {
	return manifest.WithModuleEntrypoint(args.Source), nil
}

type legacyRuntimeOpts struct {
	ModuleSource  dagql.Optional[dagql.String]
	EngineVersion dagql.Optional[dagql.String]
}

func legacyRuntimeArgs() []dagql.Argument {
	return []dagql.Argument{
		dagql.Arg("moduleSource").Doc("Module source path. The default is the manifest directory."),
		dagql.Arg("engineVersion").Doc("Required engine API version. The default is the running engine version."),
	}
}

func legacyRuntimeResolver(configure func(*core.ModuleManifest, string, string) *core.ModuleManifest) func(context.Context, *core.ModuleManifest, legacyRuntimeOpts) (*core.ModuleManifest, error) {
	return func(ctx context.Context, manifest *core.ModuleManifest, opts legacyRuntimeOpts) (*core.ModuleManifest, error) {
		return configure(manifest, opts.ModuleSource.Value.String(), opts.EngineVersion.Value.String()), nil
	}
}

func (s *moduleManifestSchema) withLegacyInclude(
	ctx context.Context,
	manifest *core.ModuleManifest,
	args struct{ Path string },
) (*core.ModuleManifest, error) {
	return manifest.WithLegacyInclude(args.Path), nil
}

func (s *moduleManifestSchema) withoutLegacyFields(
	ctx context.Context,
	manifest *core.ModuleManifest,
	args struct{},
) (*core.ModuleManifest, error) {
	return manifest.WithoutLegacyFields(), nil
}

func (s *moduleManifestSchema) validate(
	ctx context.Context,
	manifest *core.ModuleManifest,
	args struct {
		TargetEngineVersion dagql.Optional[dagql.String]
	},
) (core.Void, error) {
	return core.Void{}, manifest.Validate(args.TargetEngineVersion.Value.String())
}

func (s *moduleManifestSchema) tomlFile(
	ctx context.Context,
	manifest *core.ModuleManifest,
	args struct{},
) (dagql.ObjectResult[*core.File], error) {
	return manifest.TOMLFile(ctx)
}

func (s *moduleManifestSchema) legacyJSONFile(
	ctx context.Context,
	manifest *core.ModuleManifest,
	args struct{},
) (dagql.ObjectResult[*core.File], error) {
	return manifest.LegacyJSONFile(ctx)
}

func (s *moduleManifestSchema) directory(
	ctx context.Context,
	manifest *core.ModuleManifest,
	args struct{},
) (dagql.ObjectResult[*core.Directory], error) {
	return manifest.Directory(ctx)
}
