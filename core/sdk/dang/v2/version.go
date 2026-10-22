package dangv2

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/BurntSushi/toml"
	"github.com/vito/dang/v2/pkg/dang"

	"github.com/dagger/dagger/engine"
)

// EntrypointConfigFilename is the Dang project file that declares the
// versions a Dang module entrypoint is written against.
const EntrypointConfigFilename = "dang.toml"

// DefaultEntrypointEngineVersion is the engine version of a Dang module
// entrypoint whose dang.toml declares no Dagger version, or that has no
// dang.toml at all.
//
// Entrypoints written before the version could be declared were served at
// whatever engine ran them, v1.0.0-beta.16 to v1.0.0-beta.20: the v1 API,
// Dang 2.1 and the naming rules from before the cut at v1.0.0-beta.21. This
// is the last of those releases. It is pinned and must NOT follow
// engine.Version: an entrypoint that declares nothing would otherwise change
// meaning with every engine release.
const DefaultEntrypointEngineVersion = "v1.0.0-beta.20"

// EntrypointVersions are the versions a Dang module entrypoint is written
// against.
type EntrypointVersions struct {
	// Engine is the Dagger engine version: the module's engineVersion, which
	// selects its API view, naming rules and default caching.
	Engine string
	// Dang is the Dang language version the entrypoint source is parsed as.
	Dang dang.Version
}

// DangString is the Dang version as dang.toml writes it, e.g. "2.1".
func (v EntrypointVersions) DangString() string {
	return v.Dang.String()
}

// DefaultEntrypointVersions are the versions of an entrypoint that no
// dang.toml governs.
func DefaultEntrypointVersions() EntrypointVersions {
	return EntrypointVersions{
		Engine: DefaultEntrypointEngineVersion,
		Dang:   dang.DefaultVersion,
	}
}

// ResolveEntrypointVersions reads the versions of a Dang module entrypoint
// from the dang.toml that governs it, data, at configPath (used in errors).
// Nil data means no dang.toml governs the entrypoint.
//
// A Dagger import's `dagger = "vX.Y.Z"` is the engine version. Without one
// (no dang.toml, no Dagger import, or `dagger = true`), the engine version is
// DefaultEntrypointEngineVersion. `dang = "X.Y"` is the Dang version. Without
// one, the Dang version is dang.DefaultVersion, as Dang reads a dang.toml
// without the key; an entrypoint without a dang.toml gets the same, so that
// it does not follow dang.CurrentVersion forward either.
//
// The engine version must be one this engine can serve: not older than
// engine.MinimumModuleVersion, not newer than engine.Version.
func ResolveEntrypointVersions(ctx context.Context, configPath string, data []byte) (EntrypointVersions, error) {
	versions := DefaultEntrypointVersions()
	if data == nil {
		return versions, nil
	}

	// Decode into Dang's own types, so the keys parse and validate exactly
	// as Dang reads them.
	var cfg dang.ProjectConfig
	if _, err := toml.Decode(string(data), &cfg); err != nil {
		return versions, fmt.Errorf("parse %s: %w", configPath, err)
	}

	versions.Dang = dang.VersionForConfig(dang.ContextWithVersion(ctx, dang.DefaultVersion), &cfg)

	var declaredBy string
	for _, name := range slices.Sorted(maps.Keys(cfg.Imports)) {
		source := cfg.Imports[name]
		if source == nil || !source.Dagger.Enabled {
			continue
		}
		version := source.Dagger.VersionString()
		if version == "" {
			// `dagger = true`: no version declared.
			continue
		}
		if declaredBy != "" && version != versions.Engine {
			return versions, fmt.Errorf("%s: imports %q and %q declare different Dagger versions, %s and %s",
				configPath, declaredBy, name, versions.Engine, version)
		}
		declaredBy = name
		versions.Engine = version
	}
	versions.Engine = engine.NormalizeVersion(versions.Engine)

	// The same checks as a dagger.json engineVersion gets (see
	// modules.ParseModuleConfig and loadModuleSourceConfig), so a declared
	// version means the same thing in either place.
	if !engine.CheckVersionCompatibility(versions.Engine, engine.MinimumModuleVersion) {
		return versions, fmt.Errorf("%s: module requires dagger %s, but support for that version has been removed", configPath, versions.Engine)
	}
	if !engine.CheckMaxVersionCompatibility(versions.Engine, engine.BaseVersion(engine.Version)) {
		return versions, fmt.Errorf("%s: module requires dagger %s, but you have %s", configPath, versions.Engine, engine.Version)
	}
	return versions, nil
}

// contextWithEntrypointVersion sets the Dang version of an entrypoint's
// source. The driver evaluates a copy of the entrypoint files that no
// dang.toml governs, so the pin decides their version.
func contextWithEntrypointVersion(ctx context.Context, version string) (context.Context, error) {
	if version == "" {
		return dang.ContextWithVersion(ctx, dang.DefaultVersion), nil
	}
	v, err := dang.ParseVersion(version)
	if err != nil {
		return ctx, fmt.Errorf("module entrypoint Dang version: %w", err)
	}
	return dang.ContextWithVersion(ctx, v), nil
}
