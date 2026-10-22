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

// Compatibility gating compares *base* versions — prerelease and build
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
// declaring a version between the old and new value.
const PrereleaseViewsVersion = "v1.0.0-beta.21"

// basePrereleaseView is the API view of every v1.0.0 prerelease before
// PrereleaseViewsVersion: v1.0.0-0, v1.0.0-beta.1 to v1.0.0-beta.20, and so
// on. Those callers all got the v1.0.0 view before views kept prereleases, so
// they keep sharing one view; v1.0.0 itself is now the release's view. It is
// the last release before PrereleaseViewsVersion, so it sorts above every
// view gate set while views dropped prereleases (the newest is
// v1.0.0-beta.17) and below every gate at PrereleaseViewsVersion or later.
const basePrereleaseView = "v1.0.0-beta.20"

// IdentifierCasingVersion is the first engine version with the identifier
// casing rules: new module naming rules and the renamed core API (e.g.
// GitCommit.shortSHA). Gate on it with a view filter,
// View(AfterVersion(engine.IdentifierCasingVersion)) and
// View(BeforeVersion(engine.IdentifierCasingVersion)), in a resolver with
// core.Supports(ctx, engine.IdentifierCasingVersion), and for a module's
// declared engineVersion outside a call with APIVersionAtLeast.
//
// It must not be older than PrereleaseViewsVersion: earlier prereleases share
// one view, which a gate cannot split.
const IdentifierCasingVersion = "v1.0.0-beta.21"

// APIViewVersion returns the API view for a client that declares version.
// Modules get theirs from ModuleAPIViewVersion.
//
// From PrereleaseViewsVersion on, a view keeps the declared prerelease, so a
// view gate such as AfterVersion("v1.0.0-beta.21") tells v1.0.0-beta.21 and
// later apart from earlier prereleases, and orders them before v1.0.0. Build
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

// APIVersionAtLeast reports whether a module that declares engineVersion
// version gets the API of minVersion or later: whether its API view
// (ModuleAPIViewVersion) is at or past minVersion, comparing prereleases. It
// is a view gate, AfterVersion(minVersion), for a module's declared version
// where no call carries the view.
func APIVersionAtLeast(version, minVersion string) bool {
	return semver.Compare(ModuleAPIViewVersion(version), minVersion) >= 0
}

// ModuleAPIViewVersion returns the API view for a module that declares
// engineVersion version. It is APIViewVersion, with one exception while this
// engine is a v1.0.0 prerelease or dev build: a module that declares exactly
// v1.0.0 gets basePrereleaseView, the view of v1.0.0 prereleases before
// PrereleaseViewsVersion, rather than the release's view.
//
// Released modules declared v1.0.0 before views kept prereleases, when it got
// the same view as every beta (e.g. every sdk-helpers tag, which all SDKs
// lock, and go-sdk). Served the release's view by a beta engine, they would
// see the API past every prerelease gate, renamed under them, and locked
// versions can't be fixed. The release's own engine serves them its view, so
// the break comes with v1.0.0, not with a beta. Client versions are not
// clamped: a client's version is the dagger CLI's that started its session
// (or the engine's, for a nested client), not a pin locked into a released
// module.
func ModuleAPIViewVersion(version string) string {
	view := APIViewVersion(version)
	if view != "v1.0.0" || !isV1Prerelease(Version) {
		return view
	}
	declared := NormalizeVersion(version)
	if strings.TrimSuffix(declared, semver.Build(declared)) != "v1.0.0" {
		// a dev build of v1.0.0 is past the cut
		return view
	}
	return basePrereleaseView
}

// isV1Prerelease reports whether version is a prerelease or dev build of
// v1.0.0.
func isV1Prerelease(version string) bool {
	return BaseVersion(version) == "v1.0.0" && semver.Prerelease(version) != ""
}

func BaseVersion(version string) string {
	version = strings.TrimSuffix(version, semver.Build(version))
	version = strings.TrimSuffix(version, semver.Prerelease(version))
	return version
}
