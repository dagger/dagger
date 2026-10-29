package dangv2

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"

	"github.com/BurntSushi/toml"
	"github.com/vito/dang/v2/pkg/dang"

	"github.com/dagger/dagger/engine"
)

// ConfigFilename is the Dang project file that declares the versions a Dang
// module is written against.
const ConfigFilename = "dang.toml"

// DefaultEntrypointEngineVersion is the engine version of a Dang module
// entrypoint whose dang.toml declares no Dagger version, or that has no
// dang.toml at all.
//
// Entrypoints written before the version could be declared were served at
// whatever engine ran them, v1.0.0-beta.16 to v1.0.0-beta.21: the v1 API,
// Dang 2.1 and the naming rules from before the cut at
// engine.IdentifierCasingVersion (v1.0.0-beta.22). All of those releases
// share one API view, the pre-cut view (see engine.APIViewVersion), and this
// lands on it. It is pinned and must NOT follow engine.Version: an
// entrypoint that declares nothing would otherwise change meaning with every
// engine release.
const DefaultEntrypointEngineVersion = "v1.0.0-beta.20"

// Versions are the versions a Dang module is written against.
type Versions struct {
	// Engine is the Dagger engine version: the module's engineVersion, which
	// selects its API view, naming rules and default caching.
	Engine string
	// Dang is the Dang language version the module source is parsed as.
	Dang dang.Version
	// DeclaredEngine is the Dagger version the dang.toml declares, if any.
	// Engine is that version when set.
	DeclaredEngine string
	// DangDeclared reports whether the dang.toml declares Dang.
	DangDeclared bool
}

// DangString is the Dang version as dang.toml writes it, e.g. "2.1".
func (v Versions) DangString() string {
	return v.Dang.String()
}

// DefaultEntrypointVersions are the versions of an entrypoint that no
// dang.toml governs.
func DefaultEntrypointVersions() Versions {
	return Versions{
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
// A declared engine version must be one this engine can serve, checked as a
// dagger.json engineVersion is: not older than engine.MinimumModuleVersion,
// and no later base version than engine.Version's. A later prerelease of the
// engine's base version, or the release itself, is accepted and served the
// engine's own view (engine.ModuleAPIViewVersion caps it).
func ResolveEntrypointVersions(ctx context.Context, configPath string, data []byte) (Versions, error) {
	defaults := DefaultEntrypointVersions()
	return resolveVersions(ctx, configPath, data, defaults.Engine, func(string) dang.Version {
		return defaults.Dang
	})
}

// DefaultModuleDangVersion is the Dang version of a Dang SDK module (one that
// is not a manifest v2 entrypoint) at engine version engineVersion when no
// dang.toml declares one.
//
// Modules written for an engine before engine.MinimumDangV2ModuleVersion were
// evaluated with the Dang v1 line, so their source is Dang 1.0
// (dang.Version1_0). Later modules are not pinned: the zero Version, which
// leaves the version to Dang, i.e. dang.CurrentVersion without a dang.toml
// and dang.DefaultVersion with one that declares none, as they always were.
func DefaultModuleDangVersion(engineVersion string) dang.Version {
	if engine.CheckVersionCompatibility(
		engine.BaseVersion(engine.NormalizeVersion(engineVersion)),
		engine.MinimumDangV2ModuleVersion,
	) {
		return dang.Version{}
	}
	return dang.Version1_0
}

// DefaultModuleVersions are the versions of a Dang SDK module at engine
// version engineVersion that no dang.toml governs.
func DefaultModuleVersions(engineVersion string) Versions {
	versions, _ := resolveVersions(context.Background(), "", nil, engineVersion, DefaultModuleDangVersion) // no dang.toml: no error
	return versions
}

// ResolveModuleVersions reads the versions of a Dang SDK module from the
// dang.toml that governs its source, data, at configPath (used in errors). Nil
// data means no dang.toml governs it. engineVersion is the module's
// engineVersion as its manifest declares it (dagger.json or
// dagger-module.toml).
//
// The dang.toml wins: a Dagger import's `dagger = "vX.Y.Z"` is the engine
// version, whatever the manifest says. Without one (no dang.toml, no Dagger
// import, or `dagger = true`) the engine version is the manifest's, as the
// Dang CLI reads `dagger = true`. `dang = "X.Y"` is the Dang version; without
// one, it is DefaultModuleDangVersion of the resulting engine version, or the
// version Dang picks for the source on its own.
//
// A declared engine version must be one this engine can serve, as an
// engineVersion must.
func ResolveModuleVersions(ctx context.Context, configPath string, data []byte, engineVersion string) (Versions, error) {
	return resolveVersions(ctx, configPath, data, engineVersion, DefaultModuleDangVersion)
}

// resolveVersions resolves the versions in data (nil: no dang.toml) with
// defaultEngine as the engine version when none is declared, and
// defaultDang(engine version) as the Dang version when none is declared (the
// zero Version: Dang's own default).
func resolveVersions(
	ctx context.Context,
	configPath string,
	data []byte,
	defaultEngine string,
	defaultDang func(engineVersion string) dang.Version,
) (Versions, error) {
	versions := Versions{Engine: engine.NormalizeVersion(defaultEngine)}
	if data == nil {
		versions.Dang = defaultDang(versions.Engine)
		if versions.Dang.IsZero() {
			// No dang.toml governs the source, so Dang parses it as its
			// current version.
			versions.Dang = dang.CurrentVersion
		}
		return versions, nil
	}

	// A `dang` version too new for this engine's Dang means the module needs
	// a newer Dagger, not a newer Dang: Dang is built into the engine.
	var raw struct {
		Dang any `toml:"dang"`
	}
	if _, err := toml.Decode(string(data), &raw); err != nil {
		return versions, fmt.Errorf("parse %s: %w", configPath, err)
	}
	if s, ok := raw.Dang.(string); ok {
		if v, tooNew := dangVersionTooNew(s); tooNew {
			return versions, fmt.Errorf("%s: module requires Dang %s, but this Dagger engine supports Dang %s at most; upgrade Dagger", configPath, v, dang.CurrentVersion)
		}
	}

	// Decode into Dang's own types, so the keys parse and validate exactly
	// as Dang reads them.
	var cfg dang.ProjectConfig
	if _, err := toml.Decode(string(data), &cfg); err != nil {
		return versions, fmt.Errorf("parse %s: %w", configPath, err)
	}

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
		if declaredBy != "" && version != versions.DeclaredEngine {
			return versions, fmt.Errorf("%s: imports %q and %q declare different Dagger versions, %s and %s",
				configPath, declaredBy, name, versions.DeclaredEngine, version)
		}
		declaredBy = name
		versions.DeclaredEngine = version
	}
	if versions.DeclaredEngine != "" {
		versions.DeclaredEngine = engine.NormalizeVersion(versions.DeclaredEngine)
		versions.Engine = versions.DeclaredEngine

		// The same checks as a dagger.json engineVersion gets (see
		// modules.ParseModuleConfig and loadModuleSourceConfig), so a
		// declared version means the same thing in either place.
		if !engine.CheckVersionCompatibility(versions.Engine, engine.MinimumModuleVersion) {
			return versions, fmt.Errorf("%s: module requires dagger %s, but support for that version has been removed", configPath, versions.Engine)
		}
		if !engine.CheckMaxVersionCompatibility(versions.Engine, engine.BaseVersion(engine.Version)) {
			return versions, fmt.Errorf("%s: module requires dagger %s, but you have %s", configPath, versions.Engine, engine.Version)
		}
	}

	pin := defaultDang(versions.Engine)
	if !pin.IsZero() {
		ctx = dang.ContextWithVersion(ctx, pin)
	}
	versions.Dang = dang.VersionForConfig(ctx, &cfg)
	versions.DangDeclared = !cfg.Dang.IsZero()
	return versions, nil
}

// dangMajorMinor matches the major.minor of a version as dang.toml writes it.
var dangMajorMinor = regexp.MustCompile(`^v?(0|[1-9]\d*)\.(0|[1-9]\d*)`)

// dangVersionTooNew reports whether s is a Dang version newer than this
// engine's Dang implements, and its major.minor.
func dangVersionTooNew(s string) (dang.Version, bool) {
	m := dangMajorMinor.FindStringSubmatch(s)
	if m == nil {
		return dang.Version{}, false
	}
	major, err := strconv.Atoi(m[1])
	if err != nil {
		return dang.Version{}, false
	}
	minor, err := strconv.Atoi(m[2])
	if err != nil {
		return dang.Version{}, false
	}
	v := dang.Version{Major: major, Minor: minor}
	if v.IsZero() {
		return v, false
	}
	return v, dang.CurrentVersion.Before(v)
}

// ContextWithDangVersion sets the Dang version of a module's source, version
// as ModuleSource.DangVersion holds it (empty: Dang's own default).
func ContextWithDangVersion(ctx context.Context, version string) (context.Context, error) {
	if version == "" {
		return ctx, nil
	}
	v, err := dang.ParseVersion(version)
	if err != nil {
		return ctx, fmt.Errorf("module Dang version: %w", err)
	}
	return dang.ContextWithVersion(ctx, v), nil
}

// contextWithEntrypointVersion sets the Dang version of an entrypoint's
// source. The driver evaluates a copy of the entrypoint files that no
// dang.toml governs, so the pin decides their version.
func contextWithEntrypointVersion(ctx context.Context, version string) (context.Context, error) {
	v, err := parseEntrypointDangVersion(version)
	if err != nil {
		return ctx, err
	}
	return dang.ContextWithVersion(ctx, v), nil
}

// parseEntrypointDangVersion parses an entrypoint's Dang version as
// ModuleSource.DangVersion holds it; empty is dang.DefaultVersion.
func parseEntrypointDangVersion(version string) (dang.Version, error) {
	if version == "" {
		return dang.DefaultVersion, nil
	}
	v, err := dang.ParseVersion(version)
	if err != nil {
		return dang.Version{}, fmt.Errorf("module entrypoint Dang version: %w", err)
	}
	return v, nil
}

// entrypointDangVersion is parseEntrypointDangVersion for best-effort parses,
// which fall back to dang.DefaultVersion; running the entrypoint reports an
// invalid version.
func entrypointDangVersion(version string) dang.Version {
	v, err := parseEntrypointDangVersion(version)
	if err != nil {
		return dang.DefaultVersion
	}
	return v
}
