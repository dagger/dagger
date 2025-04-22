package tsdistconsts

const (
	DefaultNodeVersion  = "22.11.0" // LTS version, JOD (https://nodejs.org/en/about/previous-releases)
	nodeImageDigest     = "sha256:b64ced2e7cd0a4816699fe308ce6e8a08ccba463c757c00c14cd372e3d2c763e"
	DefaultNodeImageRef = "node:" + DefaultNodeVersion + "-alpine@" + nodeImageDigest

	DefaultBunVersion  = "1.1.38"
	bunImageDigest     = "sha256:c1cc397e0be452c54f37cbcdfaa747eff93c993723af7d91658764d0fdfe5873"
	DefaultBunImageRef = "oven/bun:" + DefaultBunVersion + "-alpine@" + bunImageDigest

	DefaultDenoVersion  = "2.2.4"
	denoImageDigest     = "sha256:1d8c91cb71602ac152c1a7e49654aaa9f6c9dbe8c82e43221adf913f89683987"
	DefaultDenoImageRef = "denoland/deno:alpine-" + DefaultDenoVersion + "@" + denoImageDigest
)
