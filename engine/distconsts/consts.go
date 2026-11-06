// Package consts exists to facilitate sharing values between our CI infra and
// dependent code (e.g. SDKs).
//
// These are kept separate from all other code to avoid breakage from
// backwards-incompatible changes (dev/ uses stable SDK, core/ uses dev).
package distconsts

const (
	EngineContainerName = "dagger-engine.dev"

	DefaultEngineSockAddr = "unix:///run/dagger/engine.sock"
)

const (
	RuntimePath    = "/usr/local/bin/crun"
	DaggerInitPath = "/usr/local/bin/dagger-init"
	// RuntimeStateRoot is where the runtime keeps each container's state, in
	// a directory named by the container's ID.
	RuntimeStateRoot = "/run/crun"
	// DaggerInitSessionPath is the session-attachables helper of nested
	// clients, mounted into their containers at InitSessionContainerPath.
	DaggerInitSessionPath    = "/usr/local/bin/dagger-init-session"
	InitSessionContainerPath = "/.init-session"
	// InitTimingFDEnv names the fd on which the injected /.init reports
	// when it started, spawned the command and reaped it, as CLOCK_MONOTONIC
	// nanoseconds ("<started> <spawned> <exited>\n"). The engine sets it only
	// when profiling with wcprof; /.init removes it before starting the command.
	InitTimingFDEnv = "_DAGGER_INIT_TIMING_FD"
	// SessionHelperStatusFDEnv names the fd on which the injected /.init
	// reports that a nested exec's session helper failed to start or exited
	// ("start-failed <error>\n" or "exited <status>\n").
	SessionHelperStatusFDEnv = "_DAGGER_SESSION_HELPER_STATUS_FD"
	DaggerCLIPath            = "/usr/local/bin/dagger"
	TiniPath                 = "/usr/local/bin/tini"

	EngineDefaultStateDir = "/var/lib/dagger"

	EngineContainerBuiltinContentDir   = "/usr/local/share/dagger/content"
	GoSDKManifestDigestEnvName         = "DAGGER_GO_SDK_MANIFEST_DIGEST"
	PythonSDKManifestDigestEnvName     = "DAGGER_PYTHON_SDK_MANIFEST_DIGEST"
	TypescriptSDKManifestDigestEnvName = "DAGGER_TYPESCRIPT_SDK_MANIFEST_DIGEST"

	// EngineContainerImageEntriesDir holds the entries the engine image ships,
	// which the engine merges into its cache at start: their bundle and its
	// blobs.
	EngineContainerImageEntriesDir = "/usr/local/share/dagger/entries"
)

const (
	AlpineVersion = "3.22.1"
	AlpineImage   = "alpine:" + AlpineVersion

	GolangVersion = "1.27"
	GolangImage   = "golang:" + GolangVersion + "-alpine"

	BusyboxVersion = "1.37.0"
	BusyboxImage   = "busybox:" + BusyboxVersion
)

// GoSDKBuildFlags are the go build flags the built-in Go SDK builds every Go
// module runtime with, which the engine gives it, and which the engine image's
// entries are built with, so that its builds are the runtimes' own. The link
// drops debug information, so compiles don't generate it, but for the
// standard library, whose archives stay the ones every other build reads from
// the engine image. The generated client compiles without inlining: it is
// about half of its compile's CPU, on code that makes network calls.
func GoSDKBuildFlags() []string {
	return []string{"-ldflags=-s -w", "-gcflags=all=-dwarf=false", "-gcflags=std=", "-gcflags=./internal/dagger/...=-dwarf=false -l"}
}

const (
	OCIVersionAnnotation = "org.opencontainers.image.version"
)

const (
	EngineCustomCACertsDir = "/usr/local/share/ca-certificates"
)
