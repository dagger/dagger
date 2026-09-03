package core

import (
	"bytes"
	"context"
	"fmt"
	"slices"

	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/dagql"
	toml "github.com/pelletier/go-toml"
	"github.com/vektah/gqlparser/v2/ast"
)

// ModuleManifestVersions constructs versioned module manifests.
type ModuleManifestVersions struct{}

func (*ModuleManifestVersions) Type() *ast.Type {
	return &ast.Type{NamedType: "ModuleManifestVersions", NonNull: true}
}

func (*ModuleManifestVersions) TypeDescription() string {
	return "Versioned Dagger module manifest builders."
}

// ModuleManifestV1 is a structured manifest for the current unversioned
// dagger-module.toml format.
type ModuleManifestV1 struct {
	Name          string
	EngineVersion string
	Source        string
	Runtime       string
	Include       []string
}

func (*ModuleManifestV1) Type() *ast.Type {
	return &ast.Type{NamedType: "ModuleManifestV1", NonNull: true}
}

func (*ModuleManifestV1) TypeDescription() string {
	return "A Dagger module manifest in the current unversioned format."
}

func (manifest *ModuleManifestV1) Clone() *ModuleManifestV1 {
	cloned := *manifest
	cloned.Include = slices.Clone(manifest.Include)
	return &cloned
}

func (manifest *ModuleManifestV1) WithRuntime(source string) *ModuleManifestV1 {
	manifest = manifest.Clone()
	manifest.Runtime = source
	return manifest
}

func (manifest *ModuleManifestV1) WithSource(path string) *ModuleManifestV1 {
	manifest = manifest.Clone()
	manifest.Source = path
	return manifest
}

func (manifest *ModuleManifestV1) WithEngineVersion(version string) *ModuleManifestV1 {
	manifest = manifest.Clone()
	manifest.EngineVersion = version
	return manifest
}

func (manifest *ModuleManifestV1) WithInclude(path string) *ModuleManifestV1 {
	manifest = manifest.Clone()
	manifest.Include = append(manifest.Include, path)
	return manifest
}

// Contents serializes the manifest as dagger-module.toml.
func (manifest *ModuleManifestV1) Contents() ([]byte, error) {
	if manifest == nil {
		return nil, fmt.Errorf("module manifest v1 is nil")
	}
	if manifest.Name == "" {
		return nil, fmt.Errorf("module manifest v1 name is required")
	}
	if manifest.Runtime == "" {
		return nil, fmt.Errorf("module manifest v1 runtime is required")
	}

	return modules.MarshalModuleConfigForFilename(&modules.ModuleConfigWithUserFields{
		ModuleConfig: modules.ModuleConfig{
			Name:          manifest.Name,
			EngineVersion: manifest.EngineVersion,
			Source:        manifest.Source,
			Include:       slices.Clone(manifest.Include),
			SDK:           &modules.SDK{Source: manifest.Runtime},
		},
	}, modules.Filename)
}

// AsFile serializes the manifest as a dagger-module.toml file.
func (manifest *ModuleManifestV1) AsFile(ctx context.Context) (dagql.ObjectResult[*File], error) {
	contents, err := manifest.Contents()
	if err != nil {
		return dagql.ObjectResult[*File]{}, err
	}
	return moduleManifestFile(ctx, contents)
}

// ModuleManifestV2 is a structured dagger-module.toml manifest version 2.
type ModuleManifestV2 struct {
	Name             string
	EntrypointKind   string
	EntrypointSource string
}

func (*ModuleManifestV2) Type() *ast.Type {
	return &ast.Type{NamedType: "ModuleManifestV2", NonNull: true}
}

func (*ModuleManifestV2) TypeDescription() string {
	return "A Dagger module manifest in format version 2."
}

func (manifest *ModuleManifestV2) Clone() *ModuleManifestV2 {
	cloned := *manifest
	return &cloned
}

func (manifest *ModuleManifestV2) WithDangEntrypoint(source string) *ModuleManifestV2 {
	return manifest.withEntrypoint("dang", source)
}

func (manifest *ModuleManifestV2) WithModuleEntrypoint(source string) *ModuleManifestV2 {
	return manifest.withEntrypoint("module", source)
}

func (manifest *ModuleManifestV2) withEntrypoint(kind, source string) *ModuleManifestV2 {
	manifest = manifest.Clone()
	manifest.EntrypointKind = kind
	manifest.EntrypointSource = source
	return manifest
}

type moduleManifestV2Document struct {
	ManifestVersion int                        `toml:"manifestVersion"`
	Name            string                     `toml:"name"`
	Entrypoint      moduleManifestV2Entrypoint `toml:"entrypoint"`
}

type moduleManifestV2Entrypoint struct {
	Kind   string `toml:"kind"`
	Source string `toml:"source"`
}

// Contents serializes the manifest as dagger-module.toml.
func (manifest *ModuleManifestV2) Contents() ([]byte, error) {
	if manifest == nil {
		return nil, fmt.Errorf("module manifest v2 is nil")
	}
	if manifest.Name == "" {
		return nil, fmt.Errorf("module manifest v2 name is required")
	}
	if manifest.EntrypointKind == "" || manifest.EntrypointSource == "" {
		return nil, fmt.Errorf("module manifest v2 entrypoint is required")
	}
	if manifest.EntrypointKind != "dang" && manifest.EntrypointKind != "module" {
		return nil, fmt.Errorf("module manifest v2 entrypoint kind must be dang or module")
	}

	var buf bytes.Buffer
	err := toml.NewEncoder(&buf).
		Order(toml.OrderPreserve).
		Encode(moduleManifestV2Document{
			ManifestVersion: 2,
			Name:            manifest.Name,
			Entrypoint: moduleManifestV2Entrypoint{
				Kind:   manifest.EntrypointKind,
				Source: manifest.EntrypointSource,
			},
		})
	if err != nil {
		return nil, fmt.Errorf("serialize module manifest v2: %w", err)
	}
	return buf.Bytes(), nil
}

// AsFile serializes the manifest as a dagger-module.toml file.
func (manifest *ModuleManifestV2) AsFile(ctx context.Context) (dagql.ObjectResult[*File], error) {
	contents, err := manifest.Contents()
	if err != nil {
		return dagql.ObjectResult[*File]{}, err
	}
	return moduleManifestFile(ctx, contents)
}

func moduleManifestFile(ctx context.Context, contents []byte) (dagql.ObjectResult[*File], error) {
	dag, err := CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*File]{}, err
	}
	var file dagql.ObjectResult[*File]
	err = dag.Select(ctx, dag.Root(), &file, dagql.Selector{
		Field: "file",
		Args: []dagql.NamedInput{
			{Name: "name", Value: dagql.NewString(modules.Filename)},
			{Name: "contents", Value: dagql.NewString(string(contents))},
		},
	})
	if err != nil {
		return dagql.ObjectResult[*File]{}, fmt.Errorf("create module manifest file: %w", err)
	}
	return file, nil
}
