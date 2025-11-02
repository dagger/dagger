package tsdistconsts

const (
	// NOTE: when changing this version, check if the `NpmDefaultVersion` var in sdk/typescript/runtime/config.go
	// should be updated to match the version of npm pre-installed in this container
	DefaultNodeVersion  = "22.11.0" // LTS version, JOD (https://nodejs.org/en/about/previous-releases)
	nodeImageDigest     = "sha256:b64ced2e7cd0a4816699fe308ce6e8a08ccba463c757c00c14cd372e3d2c763e"
	DefaultNodeImageRef = "node:" + DefaultNodeVersion + "-alpine@" + nodeImageDigest

	DefaultBunVersion  = "1.3.0"
	bunImageDigest     = "sha256:37e6b1cbe053939bccf6ae4507977ed957eaa6e7f275670b72ad6348e0d2c11f"
	DefaultBunImageRef = "oven/bun:" + DefaultBunVersion + "-alpine@" + bunImageDigest

	DefaultDenoVersion  = "2.5.0"
	denoImageDigest     = "sha256:8f58f398552de8ee5028b69bd92370d0703bcec220adcfc68a07669f1be241f3"
	DefaultDenoImageRef = "denoland/deno:alpine-" + DefaultDenoVersion + "@" + denoImageDigest

	DefaultAlpineVersion  = "3.22.2"
	defaultAlpineDigest   = "sha256:4b7ce07002c69e8f3d704a9c5d6fd3053be500b7f1c69fc0d80990c2ad8dd412"
	DefaultAlpineImageRef = "alpine:" + DefaultAlpineVersion + "@" + defaultAlpineDigest

	DefaultTypeScriptVersion = "5.9.3"
)
