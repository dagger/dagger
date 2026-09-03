package core

import (
	"bytes"
	"context"
	"fmt"
	"slices"

	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/core/sdk/sdkmeta"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	toml "github.com/pelletier/go-toml"
	"github.com/vektah/gqlparser/v2/ast"
)

// ModuleManifest constructs current and legacy module manifest files.
type ModuleManifest struct {
	Name string

	EntrypointKind   string
	EntrypointSource string

	LegacyRuntime       string
	LegacyEngineVersion string
	LegacyInclude       []string
}

func (*ModuleManifest) Type() *ast.Type {
	return &ast.Type{NamedType: "ModuleManifest", NonNull: true}
}

func (*ModuleManifest) TypeDescription() string {
	return "A Dagger module manifest."
}

func (manifest *ModuleManifest) Clone() *ModuleManifest {
	cloned := *manifest
	cloned.LegacyInclude = slices.Clone(manifest.LegacyInclude)
	return &cloned
}

func (manifest *ModuleManifest) WithDangEntrypoint(source string) *ModuleManifest {
	return manifest.withEntrypoint("dang", source)
}

func (manifest *ModuleManifest) WithModuleEntrypoint(source string) *ModuleManifest {
	return manifest.withEntrypoint("module", source)
}

func (manifest *ModuleManifest) withEntrypoint(kind, source string) *ModuleManifest {
	manifest = manifest.Clone()
	manifest.EntrypointKind = kind
	manifest.EntrypointSource = source
	return manifest
}

func (manifest *ModuleManifest) WithLegacyGoRuntime() *ModuleManifest {
	return manifest.withLegacyRuntime(sdkmeta.Go)
}

func (manifest *ModuleManifest) WithLegacyDangRuntime() *ModuleManifest {
	return manifest.withLegacyRuntime(sdkmeta.Dang)
}

func (manifest *ModuleManifest) WithLegacyPythonRuntime() *ModuleManifest {
	return manifest.withLegacyRuntime(sdkmeta.Python)
}

func (manifest *ModuleManifest) WithLegacyTypescriptRuntime() *ModuleManifest {
	return manifest.withLegacyRuntime(sdkmeta.Typescript)
}

func (manifest *ModuleManifest) WithLegacyPHPRuntime() *ModuleManifest {
	return manifest.withLegacyRuntime(sdkmeta.PHP)
}

func (manifest *ModuleManifest) WithLegacyElixirRuntime() *ModuleManifest {
	return manifest.withLegacyRuntime(sdkmeta.Elixir)
}

func (manifest *ModuleManifest) WithLegacyJavaRuntime() *ModuleManifest {
	return manifest.withLegacyRuntime(sdkmeta.Java)
}

func (manifest *ModuleManifest) withLegacyRuntime(runtime string) *ModuleManifest {
	manifest = manifest.Clone()
	manifest.LegacyRuntime = runtime
	if manifest.LegacyEngineVersion == "" {
		manifest.LegacyEngineVersion = engine.NormalizeVersion(engine.Version)
	}
	return manifest
}

func (manifest *ModuleManifest) WithLegacyEngineVersion(version string) *ModuleManifest {
	manifest = manifest.Clone()
	manifest.LegacyEngineVersion = normalizeManifestEngineVersion(version)
	return manifest
}

func (manifest *ModuleManifest) WithLegacyInclude(path string) *ModuleManifest {
	manifest = manifest.Clone()
	manifest.LegacyInclude = append(manifest.LegacyInclude, path)
	return manifest
}

func (manifest *ModuleManifest) WithoutLegacyFields() *ModuleManifest {
	manifest = manifest.Clone()
	manifest.LegacyRuntime = ""
	manifest.LegacyEngineVersion = ""
	manifest.LegacyInclude = nil
	return manifest
}

func (manifest *ModuleManifest) Validate(targetEngineVersion string) error {
	if manifest == nil {
		return fmt.Errorf("module manifest is nil")
	}
	if manifest.Name == "" {
		return fmt.Errorf("module manifest name is required")
	}

	hasEntrypointKind := manifest.EntrypointKind != ""
	hasEntrypointSource := manifest.EntrypointSource != ""
	if hasEntrypointKind != hasEntrypointSource {
		return fmt.Errorf("module manifest entrypoint kind and source must be set together")
	}
	if hasEntrypointKind && manifest.EntrypointKind != "dang" && manifest.EntrypointKind != "module" {
		return fmt.Errorf("module manifest entrypoint kind must be dang or module")
	}

	hasLegacyFields := manifest.LegacyEngineVersion != "" || len(manifest.LegacyInclude) > 0
	if manifest.LegacyRuntime == "" && hasLegacyFields {
		return fmt.Errorf("module manifest legacy runtime is required when legacy fields are set")
	}
	if manifest.LegacyRuntime != "" {
		if !sdkmeta.IsBuiltin(manifest.LegacyRuntime) {
			return fmt.Errorf("module manifest legacy runtime %q is not supported", manifest.LegacyRuntime)
		}
		if manifest.LegacyEngineVersion == "" {
			return fmt.Errorf("module manifest legacy engine version is required")
		}
	}

	if !hasEntrypointKind && manifest.LegacyRuntime == "" {
		return fmt.Errorf("module manifest requires an entrypoint or legacy runtime")
	}

	if targetEngineVersion != "" && manifest.LegacyRuntime != "" {
		targetEngineVersion = normalizeManifestEngineVersion(targetEngineVersion)
		if !engine.CheckMaxVersionCompatibility(manifest.LegacyEngineVersion, targetEngineVersion) {
			return fmt.Errorf(
				"module manifest legacy runtime requires dagger %s, but target engine is %s",
				manifest.LegacyEngineVersion,
				targetEngineVersion,
			)
		}
	}

	return nil
}

type moduleManifestTOMLDocument struct {
	Name          string                    `toml:"name"`
	EngineVersion string                    `toml:"engineVersion,omitempty"`
	Include       []string                  `toml:"include,omitempty"`
	Runtime       *modules.SDK              `toml:"runtime,omitempty"`
	Entrypoint    *moduleManifestEntrypoint `toml:"entrypoint,omitempty"`
}

type moduleManifestEntrypoint struct {
	Kind   string `toml:"kind"`
	Source string `toml:"source"`
}

func (manifest *ModuleManifest) TOMLContents() ([]byte, error) {
	if err := manifest.Validate(""); err != nil {
		return nil, err
	}

	document := moduleManifestTOMLDocument{
		Name:          manifest.Name,
		EngineVersion: manifest.LegacyEngineVersion,
		Include:       slices.Clone(manifest.LegacyInclude),
	}
	if manifest.LegacyRuntime != "" {
		document.Runtime = &modules.SDK{Source: manifest.LegacyRuntime}
	}
	if manifest.EntrypointKind != "" {
		document.Entrypoint = &moduleManifestEntrypoint{
			Kind:   manifest.EntrypointKind,
			Source: manifest.EntrypointSource,
		}
	}

	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Order(toml.OrderPreserve).Encode(document); err != nil {
		return nil, fmt.Errorf("serialize module manifest TOML: %w", err)
	}
	return normalizeModuleManifestContents(buf.Bytes()), nil
}

func (manifest *ModuleManifest) LegacyJSONContents() ([]byte, error) {
	if err := manifest.Validate(""); err != nil {
		return nil, err
	}
	if manifest.LegacyRuntime == "" {
		return nil, fmt.Errorf("module manifest has no legacy runtime")
	}

	return modules.MarshalModuleConfigForFilename(&modules.ModuleConfigWithUserFields{
		ModuleConfig: modules.ModuleConfig{
			Name:          manifest.Name,
			EngineVersion: manifest.LegacyEngineVersion,
			Include:       slices.Clone(manifest.LegacyInclude),
			SDK:           &modules.SDK{Source: manifest.LegacyRuntime},
		},
	}, modules.LegacyFilename)
}

func (manifest *ModuleManifest) TOMLFile(ctx context.Context) (dagql.ObjectResult[*File], error) {
	contents, err := manifest.TOMLContents()
	if err != nil {
		return dagql.ObjectResult[*File]{}, err
	}
	return moduleManifestFile(ctx, modules.Filename, contents)
}

func (manifest *ModuleManifest) LegacyJSONFile(ctx context.Context) (dagql.ObjectResult[*File], error) {
	contents, err := manifest.LegacyJSONContents()
	if err != nil {
		return dagql.ObjectResult[*File]{}, err
	}
	return moduleManifestFile(ctx, modules.LegacyFilename, contents)
}

func (manifest *ModuleManifest) Directory(ctx context.Context) (dagql.ObjectResult[*Directory], error) {
	tomlContents, err := manifest.TOMLContents()
	if err != nil {
		return dagql.ObjectResult[*Directory]{}, err
	}

	files := []moduleManifestOutputFile{{
		Name:     modules.Filename,
		Contents: tomlContents,
	}}
	if manifest.LegacyRuntime != "" {
		legacyJSONContents, err := manifest.LegacyJSONContents()
		if err != nil {
			return dagql.ObjectResult[*Directory]{}, err
		}
		files = append(files, moduleManifestOutputFile{
			Name:     modules.LegacyFilename,
			Contents: legacyJSONContents,
		})
	}

	return moduleManifestDirectory(ctx, files)
}

type moduleManifestOutputFile struct {
	Name     string
	Contents []byte
}

func moduleManifestFile(ctx context.Context, name string, contents []byte) (dagql.ObjectResult[*File], error) {
	dag, err := CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*File]{}, err
	}
	var file dagql.ObjectResult[*File]
	err = dag.Select(ctx, dag.Root(), &file, dagql.Selector{
		Field: "file",
		Args: []dagql.NamedInput{
			{Name: "name", Value: dagql.NewString(name)},
			{Name: "contents", Value: dagql.NewString(string(contents))},
		},
	})
	if err != nil {
		return dagql.ObjectResult[*File]{}, fmt.Errorf("create module manifest file: %w", err)
	}
	return file, nil
}

func moduleManifestDirectory(ctx context.Context, files []moduleManifestOutputFile) (dagql.ObjectResult[*Directory], error) {
	dag, err := CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*Directory]{}, err
	}

	selectors := []dagql.Selector{{Field: "directory"}}
	for _, file := range files {
		selectors = append(selectors, dagql.Selector{
			Field: "withNewFile",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.NewString(file.Name)},
				{Name: "contents", Value: dagql.NewString(string(file.Contents))},
				{Name: "permissions", Value: dagql.Int(0o644)},
			},
		})
	}

	var directory dagql.ObjectResult[*Directory]
	if err := dag.Select(ctx, dag.Root(), &directory, selectors...); err != nil {
		return dagql.ObjectResult[*Directory]{}, fmt.Errorf("create module manifest directory: %w", err)
	}
	return directory, nil
}

func normalizeManifestEngineVersion(version string) string {
	if version == modules.EngineVersionLatest || version == "current" {
		version = engine.Version
	}
	return engine.NormalizeVersion(version)
}

func normalizeModuleManifestContents(contents []byte) []byte {
	contents = bytes.TrimRight(contents, "\n")
	return append(contents, '\n')
}
