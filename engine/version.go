package engine

import (
	"os"
	"strings"

	"golang.org/x/mod/semver"

	iversion "github.com/dagger/dagger/internal/version"
)

var (
	// Version is the engine/CLI semver, derived from internal/version.Version
	// (which embeds the VERSION file at build time) with a "v" prefix. It is no
	// longer injected via -ldflags: the binary self-reports from embedded VERSION.
	//
	// DAGGER_VERSION overrides at init for tests.
	Version string

	// Tag is the default engine image tag. It defaults to Version.
	//
	// DAGGER_TAG overrides at init for tests.
	Tag string

	// MinimumEngineVersion is used by the client to determine the minimum
	// allowed engine version that can be used by that client.
	MinimumEngineVersion = "v0.19.0"

	// MinimumClientVersion is used by the engine to determine the minimum
	// allowed client version that can connect to that engine.
	MinimumClientVersion = "v0.19.0"

	// MinimumModuleVersion is used by the engine to determine the minimum
	// allowed module engine version that can connect to this engine.
	//
	// Set to v0.9.9, because this was when the engineVersion field was
	// introduced - if it's present and not a dev version, it must be higher
	// than v0.9.9.
	MinimumModuleVersion = "v0.9.9"

	// MinimumDefaultFunctionCachingModuleVersion is the minimum module version at which
	// we will enable default function caching behavior.
	MinimumDefaultFunctionCachingModuleVersion = "v0.19.4"

	// MinimumDangV2ModuleVersion is the minimum module engine version that gets
	// Dang v2 semantics (`.{ }` is dot-block application, `.{{ }}` is
	// selection); older modules keep Dang v1 semantics (`.{ }` is selection).
	MinimumDangV2ModuleVersion = "v0.21.5"
)

var (
	presemverModuleVersion = "v0.11.9"

	versionOverridden bool
)

func FullVersion() string {
	if versionOverridden {
		return Version
	}
	return iversion.Version(iversion.WithV(), iversion.WithCommit())
}

func init() {
	if Version == "" {
		Version = iversion.Version(iversion.WithV())
	}

	// hack: dynamically populate version env vars
	// we use these during tests, but not really for anything else - this is
	// why it's okay to skip the previous validation
	if v, ok := os.LookupEnv(DaggerVersionEnv); ok {
		Version = cleanVersion(v)
		versionOverridden = true
	}
	if Tag == "" {
		Tag = Version
	}
	if v, ok := os.LookupEnv(DaggerTagEnv); ok {
		Tag = v
	}

	// The minimum version is greater than our current version this is weird,
	// and shouldn't generally be intentional - but can happen if we set it to
	// vX.Y.Z in anticipation of the next release being vX.Y.Z.
	//
	// To avoid this causing huge issues in dev builds no longer being able
	// to connect to each other, in this scenario, we cap the minVersion at
	// the current version.
	if semver.Compare(Version, MinimumClientVersion) < 0 {
		MinimumClientVersion = Version
	}
	if semver.Compare(Version, MinimumEngineVersion) < 0 {
		MinimumEngineVersion = Version
	}
	if semver.Compare(Version, MinimumModuleVersion) < 0 {
		MinimumModuleVersion = Version
	}

	if v, ok := os.LookupEnv(DaggerMinimumVersionEnv); ok {
		MinimumClientVersion = cleanVersion(v)
		MinimumEngineVersion = cleanVersion(v)
		MinimumModuleVersion = cleanVersion(v)
	}
}

func cleanVersion(v string) string {
	if semver.IsValid("v" + v) {
		return "v" + v
	}
	return v
}

// Minimum-version gating compares *base* versions — prerelease and build
// metadata stripped (see BaseVersion) — NOT strict semver precedence.
//
// Why: dev builds carry a prerelease suffix (e.g. "v0.2.0-dev-<commit>"). Under
// strict semver a prerelease ranks *below* its release and is *ordered against
// other prereleases of the same base*. That has two unwanted effects for our
// engine<->client<->module handshake:
//   - two dev builds cut from the same base version would reject each other
//     (their suffixes differ), and
//   - a dev build would be considered "older" than the release it was cut from,
//     failing a ">= that release" gate against itself.
//
// Collapsing every prerelease of vX.Y.Z to vX.Y.Z fixes both. The deliberate
// trade-off a maintainer must be aware of: this makes ALL prereleases — rc,
// alpha, beta, dev — interchangeable with the release for gating, so e.g.
// "v1.0.0-rc1" satisfies ">= v1.0.0". Compatibility here is about the base
// version's API surface, not prerelease staging. If you ever need prerelease
// ordering to matter for a gate, do NOT change these functions — that gate
// belongs elsewhere. Different *base* versions still compare normally
// (v2.0.0 and v2.0.1 remain distinct).

func CheckVersionCompatibility(version string, minVersion string) bool {
	return semver.Compare(BaseVersion(version), BaseVersion(minVersion)) >= 0
}

// CheckMaxVersionCompatibility reports whether version's base version is not
// past maxVersion's. An engine refuses a module declaring a later base version
// (e.g. v2.0.1 on v2.0.0). A later prerelease of the engine's own base
// version, or the release itself, loads: ModuleAPIViewVersion caps its view
// at the engine's.
func CheckMaxVersionCompatibility(version string, maxVersion string) bool {
	return semver.Compare(BaseVersion(version), BaseVersion(maxVersion)) <= 0
}

func NormalizeVersion(version string) string {
	version = cleanVersion(version)
	switch {
	case version == "":
		// if the target version is empty, this is weird, but probably because
		// someone did a manual build - so just assume they know what they're
		// doing, and set it to be the latest we know about
		return Version
	case !semver.IsValid(version):
		// older versions of dagger don't all use semver, so if it's a
		// non-semver version, assume it's v0.11.9 (not perfect, but it's a
		// pretty good guess)
		return presemverModuleVersion
	default:
		return version
	}
}

// PrereleaseViewsVersion is the first release whose API view keeps its
// prerelease (see APIViewVersion). It is frozen once released: moving it
// would change the view, and so the schema and cache keys, of every caller
// declaring a version between the old and new value. Until then, if the
// release it names ships without prerelease views (as v1.0.0-beta.21 did),
// bump it and basePrereleaseView together (see
// internal-docs/version-gating.md).
const PrereleaseViewsVersion = "v1.0.0-beta.22"

// basePrereleaseView is the API view of every v1.0.0 prerelease before
// PrereleaseViewsVersion: v1.0.0-0, v1.0.0-beta.1, and every later beta up to
// basePrereleaseView itself. Those callers all got the v1.0.0 view before
// views kept prereleases, so they keep sharing one view; v1.0.0 itself is now
// the release's view. It is the last release before PrereleaseViewsVersion
// (the beta just before it), so it sorts above every view gate set while
// views dropped prereleases (the newest is v1.0.0-beta.17) and below every
// gate at PrereleaseViewsVersion or later.
const basePrereleaseView = "v1.0.0-beta.21"

// IdentifierCasingVersion is the first engine version with the identifier
// casing rules: new module naming rules and the core API renamed to the
// GraphQL casing convention (e.g. GitRef.commitSha). Gate on it with a view
// filter, View(AfterVersion(engine.IdentifierCasingVersion)) and
// View(BeforeVersion(engine.IdentifierCasingVersion)), in a resolver with
// core.Supports(ctx, engine.IdentifierCasingVersion), and for a declared
// version outside a call with APIVersionAtLeast.
//
// It must not be older than PrereleaseViewsVersion: earlier prereleases share
// one view, which a gate cannot split. Prerelease views exist for this cut,
// so the two ship together; if PrereleaseViewsVersion ships before the
// casing changes do, give this its own, later, value.
const IdentifierCasingVersion = PrereleaseViewsVersion

// APIViewVersion returns the API view for a client or module that declares
// version.
//
// From PrereleaseViewsVersion on, a view keeps the declared prerelease, so a
// view gate such as AfterVersion(PrereleaseViewsVersion) tells it and later
// prereleases apart from earlier ones, and orders them before v1.0.0. Build
// metadata is dropped, and so is a dev build's prerelease (any prerelease
// mentioning "dev", e.g. v1.0.0-dev-<commit>): dev builds of a version share
// its release's view, and are past every prerelease gate of that version.
//
// Before PrereleaseViewsVersion, views are base-version granular, as they
// always were: every prerelease of a version shares that version's view.
// The exception is v1.0.0, whose release view is now distinct: its earlier
// prereleases share basePrereleaseView instead.
func APIViewVersion(version string) string {
	version = NormalizeVersion(version)
	version = strings.TrimSuffix(version, semver.Build(version))
	pre := semver.Prerelease(version)
	switch {
	case pre == "":
		return version
	case strings.Contains(pre, "dev"):
		return BaseVersion(version)
	case semver.Compare(version, PrereleaseViewsVersion) >= 0:
		return version
	case BaseVersion(version) == BaseVersion(PrereleaseViewsVersion):
		return basePrereleaseView
	default:
		return BaseVersion(version)
	}
}

// ModuleAPIViewVersion returns the API view for a module that declares
// engineVersion version: its APIViewVersion, capped at the engine's own
// (APIViewVersion(Version)). A module declaring a later prerelease than the
// engine, or the release itself, loads and is served the engine's view, the
// newest API the engine has; e.g. a v1.0.0-beta.22 engine serves a module
// declaring v1.0.0 the v1.0.0-beta.22 view. A later base version is refused
// before it gets a view (CheckMaxVersionCompatibility).
//
// Use it wherever a module's engineVersion becomes a view: its core schema,
// its client sessions, and gates on its declared version (APIVersionAtLeast).
// A module client that declares its own version is capped by it too.
func ModuleAPIViewVersion(version string) string {
	view := APIViewVersion(version)
	if engineView := APIViewVersion(Version); semver.Compare(view, engineView) > 0 {
		return engineView
	}
	return view
}

// APIVersionAtLeast reports whether a module that declares engineVersion
// version gets the API of minVersion or later: whether its API view
// (ModuleAPIViewVersion) is at or past minVersion, comparing prereleases. It
// is a view gate, AfterVersion(minVersion), for a module's declared version
// where no call carries the view.
func APIVersionAtLeast(version, minVersion string) bool {
	return semver.Compare(ModuleAPIViewVersion(version), minVersion) >= 0
}

func BaseVersion(version string) string {
	version = strings.TrimSuffix(version, semver.Build(version))
	version = strings.TrimSuffix(version, semver.Prerelease(version))
	return version
}
