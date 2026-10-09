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
	RuncPath       = "/usr/local/bin/runc"
	DaggerInitPath = "/usr/local/bin/dagger-init"
	// DaggerInitSessionPath is the session-attachables helper of nested
	// clients, mounted into their containers at InitSessionContainerPath.
	DaggerInitSessionPath    = "/usr/local/bin/dagger-init-session"
	InitSessionContainerPath = "/.init-session"
	// InitTimingFDEnv names the fd on which the injected /.init reports
	// when it started, spawned the command and reaped it, as CLOCK_MONOTONIC
	// nanoseconds ("<started> <spawned> <exited>\n"). The engine sets it only
	// when profiling with wcprof; /.init removes it before starting the command.
	InitTimingFDEnv = "_DAGGER_INIT_TIMING_FD"
	DaggerCLIPath   = "/usr/local/bin/dagger"
	TiniPath        = "/usr/local/bin/tini"

	EngineDefaultStateDir = "/var/lib/dagger"

	EngineContainerBuiltinContentDir   = "/usr/local/share/dagger/content"
	GoSDKManifestDigestEnvName         = "DAGGER_GO_SDK_MANIFEST_DIGEST"
	PythonSDKManifestDigestEnvName     = "DAGGER_PYTHON_SDK_MANIFEST_DIGEST"
	TypescriptSDKManifestDigestEnvName = "DAGGER_TYPESCRIPT_SDK_MANIFEST_DIGEST"
)

const (
	AlpineVersion = "3.22.1"
	AlpineImage   = "alpine:" + AlpineVersion

	GolangVersion = "1.26"
	GolangImage   = "golang:" + GolangVersion + "-alpine"

	BusyboxVersion = "1.37.0"
	BusyboxImage   = "busybox:" + BusyboxVersion
)

const (
	OCIVersionAnnotation = "org.opencontainers.image.version"
)

const (
	EngineCustomCACertsDir = "/usr/local/share/ca-certificates"
)
