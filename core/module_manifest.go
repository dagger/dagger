package core

import (
	"bytes"
	"context"
	"fmt"
	"maps"
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

	LegacyRuntime             string
	LegacyModuleSource        string
	LegacyEngineVersion       string
	LegacyInclude             []string
	LegacyRuntimeDependencies []*modules.ModuleConfigDependency

	TOMLConfig       *modules.ModuleConfigWithUserFields
	LegacyJSONConfig *modules.ModuleConfigWithUserFields
	LoadedLegacyJSON bool
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
	cloned.LegacyRuntimeDependencies = cloneManifestDependencies(manifest.LegacyRuntimeDependencies)
	cloned.TOMLConfig = cloneManifestConfig(manifest.TOMLConfig)
	cloned.LegacyJSONConfig = cloneManifestConfig(manifest.LegacyJSONConfig)
	return &cloned
}

func (manifest *ModuleManifest) LoadTOML(contents []byte) error {
	cfg, err := modules.ParseModuleConfigForFilename(contents, modules.Filename)
	if err != nil {
		return fmt.Errorf("load dagger-module.toml: %w", err)
	}
	var document struct {
		Entrypoint *moduleManifestEntrypoint `toml:"entrypoint"`
	}
	if err := toml.Unmarshal(contents, &document); err != nil {
		return fmt.Errorf("load dagger-module.toml entrypoint: %w", err)
	}

	manifest.TOMLConfig = cloneManifestConfig(cfg)
	if manifest.LegacyJSONConfig == nil {
		manifest.LegacyJSONConfig = cloneManifestConfig(cfg)
	}
	manifest.loadCanonicalConfig(cfg)
	if document.Entrypoint != nil {
		manifest.EntrypointKind = document.Entrypoint.Kind
		manifest.EntrypointSource = document.Entrypoint.Source
	}
	return nil
}

func (manifest *ModuleManifest) LoadJSON(contents []byte) error {
	cfg, err := modules.ParseModuleConfigForFilename(contents, modules.LegacyFilename)
	if err != nil {
		return fmt.Errorf("load dagger.json: %w", err)
	}
	manifest.LegacyJSONConfig = cloneManifestConfig(cfg)
	manifest.LoadedLegacyJSON = true
	if manifest.TOMLConfig == nil {
		manifest.TOMLConfig = cloneManifestConfig(cfg)
		manifest.loadCanonicalConfig(cfg)
	}
	return nil
}

func (manifest *ModuleManifest) loadCanonicalConfig(cfg *modules.ModuleConfigWithUserFields) {
	manifest.Name = cfg.Name
	manifest.LegacyModuleSource = cfg.Source
	manifest.LegacyEngineVersion = cfg.EngineVersion
	manifest.LegacyInclude = slices.Clone(cfg.Include)
	manifest.LegacyRuntimeDependencies = cloneManifestDependencies(cfg.Dependencies)
	manifest.LegacyRuntime = ""
	if cfg.SDK != nil {
		manifest.LegacyRuntime = cfg.SDK.Source
	}
}

func (manifest *ModuleManifest) WithName(name string) *ModuleManifest {
	manifest = manifest.Clone()
	manifest.Name = name
	return manifest
}

func (manifest *ModuleManifest) WithLegacyRuntimeDependency(name, source, pin string) *ModuleManifest {
	manifest = manifest.Clone()
	dependency := &modules.ModuleConfigDependency{Name: name, Source: source, Pin: pin}
	for index, configured := range manifest.LegacyRuntimeDependencies {
		if configured.Name == name && (name != "" || configured.Source == source) {
			manifest.LegacyRuntimeDependencies[index] = dependency
			return manifest
		}
	}
	manifest.LegacyRuntimeDependencies = append(manifest.LegacyRuntimeDependencies, dependency)
	slices.SortFunc(manifest.LegacyRuntimeDependencies, func(left, right *modules.ModuleConfigDependency) int {
		leftKey := left.Name
		if leftKey == "" {
			leftKey = left.Source
		}
		rightKey := right.Name
		if rightKey == "" {
			rightKey = right.Source
		}
		return bytes.Compare([]byte(leftKey), []byte(rightKey))
	})
	return manifest
}

func (manifest *ModuleManifest) WithoutLegacyRuntimeDependency(name string) *ModuleManifest {
	manifest = manifest.Clone()
	manifest.LegacyRuntimeDependencies = slices.DeleteFunc(manifest.LegacyRuntimeDependencies, func(dependency *modules.ModuleConfigDependency) bool {
		return dependency.Name == name || dependency.Name == "" && dependency.Source == name
	})
	return manifest
}

func (manifest *ModuleManifest) WithoutLegacyRuntimeDependencies() *ModuleManifest {
	manifest = manifest.Clone()
	manifest.LegacyRuntimeDependencies = nil
	return manifest
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

func (manifest *ModuleManifest) WithLegacyGoRuntime(moduleSource, engineVersion string) *ModuleManifest {
	return manifest.withLegacyRuntime(sdkmeta.Go, moduleSource, engineVersion)
}

func (manifest *ModuleManifest) WithLegacyDangRuntime(moduleSource, engineVersion string) *ModuleManifest {
	return manifest.withLegacyRuntime(sdkmeta.Dang, moduleSource, engineVersion)
}

func (manifest *ModuleManifest) WithLegacyPythonRuntime(moduleSource, engineVersion string) *ModuleManifest {
	return manifest.withLegacyRuntime(sdkmeta.Python, moduleSource, engineVersion)
}

func (manifest *ModuleManifest) WithLegacyTypescriptRuntime(moduleSource, engineVersion string) *ModuleManifest {
	return manifest.withLegacyRuntime(sdkmeta.Typescript, moduleSource, engineVersion)
}

func (manifest *ModuleManifest) WithLegacyPHPRuntime(moduleSource, engineVersion string) *ModuleManifest {
	return manifest.withLegacyRuntime(sdkmeta.PHP, moduleSource, engineVersion)
}

func (manifest *ModuleManifest) WithLegacyElixirRuntime(moduleSource, engineVersion string) *ModuleManifest {
	return manifest.withLegacyRuntime(sdkmeta.Elixir, moduleSource, engineVersion)
}

func (manifest *ModuleManifest) WithLegacyJavaRuntime(moduleSource, engineVersion string) *ModuleManifest {
	return manifest.withLegacyRuntime(sdkmeta.Java, moduleSource, engineVersion)
}

func (manifest *ModuleManifest) withLegacyRuntime(runtime, moduleSource, engineVersion string) *ModuleManifest {
	manifest = manifest.Clone()
	manifest.LegacyRuntime = runtime
	manifest.LegacyModuleSource = moduleSource
	if engineVersion == "" {
		engineVersion = engine.Version
	}
	manifest.LegacyEngineVersion = normalizeManifestEngineVersion(engineVersion)
	manifest.LoadedLegacyJSON = true
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
	manifest.LegacyModuleSource = ""
	manifest.LegacyEngineVersion = ""
	manifest.LegacyInclude = nil
	manifest.LegacyRuntimeDependencies = nil
	manifest.LoadedLegacyJSON = false
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

	hasLegacyFields := manifest.LegacyModuleSource != "" || manifest.LegacyEngineVersion != "" || len(manifest.LegacyInclude) > 0 || len(manifest.LegacyRuntimeDependencies) > 0
	if manifest.LegacyRuntime == "" && hasLegacyFields {
		return fmt.Errorf("module manifest legacy runtime is required when legacy fields are set")
	}
	if manifest.LegacyRuntime != "" {
		if !sdkmeta.IsBuiltin(manifest.LegacyRuntime) && manifest.TOMLConfig == nil && manifest.LegacyJSONConfig == nil {
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

type moduleManifestEntrypoint struct {
	Kind   string `toml:"kind"`
	Source string `toml:"source"`
}

func (manifest *ModuleManifest) TOMLContents() ([]byte, error) {
	if err := manifest.Validate(""); err != nil {
		return nil, err
	}

	contents, err := modules.MarshalModuleConfigForFilename(manifest.currentConfig(), modules.Filename)
	if err != nil {
		return nil, fmt.Errorf("serialize module manifest TOML: %w", err)
	}
	if manifest.EntrypointKind == "" {
		return contents, nil
	}

	document := struct {
		Entrypoint *moduleManifestEntrypoint `toml:"entrypoint"`
	}{
		Entrypoint: &moduleManifestEntrypoint{
			Kind:   manifest.EntrypointKind,
			Source: manifest.EntrypointSource,
		},
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Order(toml.OrderPreserve).Encode(document); err != nil {
		return nil, fmt.Errorf("serialize module manifest TOML entrypoint: %w", err)
	}
	contents = bytes.TrimRight(contents, "\n")
	contents = append(contents, '\n', '\n')
	contents = append(contents, bytes.TrimLeft(buf.Bytes(), "\n")...)
	return normalizeModuleManifestContents(contents), nil
}

func (manifest *ModuleManifest) LegacyJSONContents() ([]byte, error) {
	if err := manifest.Validate(""); err != nil {
		return nil, err
	}
	if manifest.LegacyRuntime == "" && !manifest.LoadedLegacyJSON {
		return nil, fmt.Errorf("module manifest has no legacy runtime")
	}

	return modules.MarshalModuleConfigForFilename(manifest.legacyConfig(), modules.LegacyFilename)
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
	if manifest.LegacyRuntime != "" || manifest.LoadedLegacyJSON {
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

func (manifest *ModuleManifest) currentConfig() *modules.ModuleConfigWithUserFields {
	config := cloneManifestConfig(manifest.TOMLConfig)
	if config == nil {
		config = &modules.ModuleConfigWithUserFields{}
	}
	manifest.applyCommonFields(config)
	return config
}

func (manifest *ModuleManifest) legacyConfig() *modules.ModuleConfigWithUserFields {
	config := cloneManifestConfig(manifest.LegacyJSONConfig)
	if config == nil {
		config = &modules.ModuleConfigWithUserFields{}
	}
	manifest.applyCommonFields(config)
	return config
}

func (manifest *ModuleManifest) applyCommonFields(config *modules.ModuleConfigWithUserFields) {
	config.Name = manifest.Name
	config.Source = manifest.LegacyModuleSource
	config.EngineVersion = manifest.LegacyEngineVersion
	config.Include = slices.Clone(manifest.LegacyInclude)
	config.Dependencies = cloneManifestDependencies(manifest.LegacyRuntimeDependencies)
	if manifest.LegacyRuntime == "" {
		config.SDK = nil
	} else if config.SDK == nil || config.SDK.Source != manifest.LegacyRuntime {
		config.SDK = &modules.SDK{Source: manifest.LegacyRuntime}
	}
}

func cloneManifestConfig(config *modules.ModuleConfigWithUserFields) *modules.ModuleConfigWithUserFields {
	if config == nil {
		return nil
	}
	cloned := *config
	cloned.SDK = cloneManifestSDK(config.SDK)
	cloned.Blueprint = cloneManifestDependency(config.Blueprint)
	cloned.Toolchains = cloneManifestDependencies(config.Toolchains)
	cloned.Include = slices.Clone(config.Include)
	cloned.Dependencies = cloneManifestDependencies(config.Dependencies)
	cloned.Codegen = nil
	if config.Codegen != nil {
		cloned.Codegen = config.Codegen.Clone()
	}
	cloned.Clients = make([]*modules.ModuleConfigClient, 0, len(config.Clients))
	for _, client := range config.Clients {
		if client != nil {
			cloned.Clients = append(cloned.Clients, client.Clone())
		}
	}
	if config.DisableDefaultFunctionCaching != nil {
		value := *config.DisableDefaultFunctionCaching
		cloned.DisableDefaultFunctionCaching = &value
	}
	return &cloned
}

func cloneManifestSDK(sdk *modules.SDK) *modules.SDK {
	if sdk == nil {
		return nil
	}
	cloned := *sdk
	return &cloned
}

func cloneManifestDependencies(dependencies []*modules.ModuleConfigDependency) []*modules.ModuleConfigDependency {
	if len(dependencies) == 0 {
		return nil
	}
	cloned := make([]*modules.ModuleConfigDependency, 0, len(dependencies))
	for _, dependency := range dependencies {
		if dependency != nil {
			cloned = append(cloned, cloneManifestDependency(dependency))
		}
	}
	return cloned
}

func cloneManifestDependency(dependency *modules.ModuleConfigDependency) *modules.ModuleConfigDependency {
	if dependency == nil {
		return nil
	}
	cloned := *dependency
	cloned.Customizations = make([]*modules.ModuleConfigArgument, 0, len(dependency.Customizations))
	for _, customization := range dependency.Customizations {
		if customization == nil {
			continue
		}
		clonedCustomization := *customization
		clonedCustomization.Function = slices.Clone(customization.Function)
		clonedCustomization.Ignore = slices.Clone(customization.Ignore)
		cloned.Customizations = append(cloned.Customizations, &clonedCustomization)
	}
	cloned.IgnoreChecks = slices.Clone(dependency.IgnoreChecks)
	cloned.IgnoreGenerators = slices.Clone(dependency.IgnoreGenerators)
	cloned.IgnoreServices = slices.Clone(dependency.IgnoreServices)
	cloned.PortMappings = maps.Clone(dependency.PortMappings)
	for name, ports := range cloned.PortMappings {
		cloned.PortMappings[name] = slices.Clone(ports)
	}
	return &cloned
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
