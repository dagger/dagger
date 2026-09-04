package schema

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

type moduleManifestSchema struct{}

var _ SchemaResolvers = &moduleManifestSchema{}

func (s *moduleManifestSchema) Install(dag *dagql.Server) {
	dagql.Fields[*core.Query]{
		dagql.Func("moduleManifest", s.moduleManifest).
			View(AfterVersion("v1.0.0-0")).
			Doc("Construct or load a module manifest.").
			Args(
				dagql.Arg("loadTOML").Doc("Optional dagger-module.toml file to load."),
				dagql.Arg("loadJSON").Doc("Optional dagger.json file to load."),
			),
	}.Install(dag)

	dagql.Fields[*core.ModuleManifest]{
		dagql.Func("withName", s.withName).
			Doc("Set the module name.").
			Args(dagql.Arg("name").Doc("Module name.")),
		dagql.Func("withDependency", s.withDependency).
			Doc("Add or replace a module dependency.").
			Args(
				dagql.Arg("source").Doc("Dependency source address."),
				dagql.Arg("name").Doc("Optional dependency name."),
				dagql.Arg("pin").Doc("Optional dependency pin."),
			),
		dagql.Func("withoutDependency", s.withoutDependency).
			Doc("Remove a module dependency by name.").
			Args(dagql.Arg("name").Doc("Dependency name.")),
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
	_ *core.Query,
	args struct {
		LoadTOML dagql.Optional[core.FileID] `name:"loadTOML"`
		LoadJSON dagql.Optional[core.FileID] `name:"loadJSON"`
	},
) (*core.ModuleManifest, error) {
	manifest := &core.ModuleManifest{}
	if args.LoadJSON.Valid {
		contents, err := loadModuleManifestFile(ctx, args.LoadJSON.Value)
		if err != nil {
			return nil, fmt.Errorf("load JSON module manifest: %w", err)
		}
		if err := manifest.LoadJSON(contents); err != nil {
			return nil, err
		}
	}
	if args.LoadTOML.Valid {
		contents, err := loadModuleManifestFile(ctx, args.LoadTOML.Value)
		if err != nil {
			return nil, fmt.Errorf("load TOML module manifest: %w", err)
		}
		if err := manifest.LoadTOML(contents); err != nil {
			return nil, err
		}
	}
	return manifest, nil
}

func loadModuleManifestFile(ctx context.Context, id core.FileID) ([]byte, error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	file, err := id.Load(ctx, dag)
	if err != nil {
		return nil, err
	}
	return file.Self().Contents(ctx, file, nil, nil)
}

func (s *moduleManifestSchema) withName(
	ctx context.Context,
	manifest *core.ModuleManifest,
	args struct{ Name string },
) (*core.ModuleManifest, error) {
	return manifest.WithName(args.Name), nil
}

func (s *moduleManifestSchema) withDependency(
	ctx context.Context,
	manifest *core.ModuleManifest,
	args struct {
		Source string
		Name   dagql.Optional[dagql.String]
		Pin    dagql.Optional[dagql.String]
	},
) (*core.ModuleManifest, error) {
	return manifest.WithDependency(args.Name.Value.String(), args.Source, args.Pin.Value.String()), nil
}

func (s *moduleManifestSchema) withoutDependency(
	ctx context.Context,
	manifest *core.ModuleManifest,
	args struct{ Name string },
) (*core.ModuleManifest, error) {
	return manifest.WithoutDependency(args.Name), nil
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
