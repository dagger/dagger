// Package consts exists to facilitate sharing values between our CI infra and
// dependent code (e.g. SDKs).
//
// These are kept separate from all other code to avoid breakage from
// backwards-incompatible changes (ci/ uses stable SDK, core/ uses dev).
package distconsts

const (
	RuncPath     = "/usr/local/bin/runc"
	DumbInitPath = "/usr/local/bin/dumb-init"

	EngineDefaultStateDir = "/var/lib/dagger"

	EngineContainerBuiltinContentDir   = "/usr/local/share/dagger/content"
	GoSDKManifestDigestEnvName         = "DAGGER_GO_SDK_MANIFEST_DIGEST"
	PythonSDKManifestDigestEnvName     = "DAGGER_PYTHON_SDK_MANIFEST_DIGEST"
	TypescriptSDKManifestDigestEnvName = "DAGGER_TYPESCRIPT_SDK_MANIFEST_DIGEST"
)

const (
	// https://hub.docker.com/_/alpine/tags
	AlpineVersion = "3.20.0"
	// We are pinning to a specific digest so that we:
	// - only pull this image once, and don't check with the registry if the tag got updated in the meantime (plays nice with offline!)
	// - know exactly which version we are using (tags are mutable)
	AlpineDigest = "sha256:77726ef6b57ddf65bb551896826ec38bc3e53f75cdde31354fbffb4f25238ebd" // crane digest ...
	AlpineImage  = "alpine:" + AlpineVersion + "@" + AlpineDigest

	// https://images.chainguard.dev/directory/image/wolfi-base/versions
	WolfiVersion = "latest" // `1` requires us to contact for access. I think that we should.
	// We really don't want `latest` changing between builds without us knowing.
	// In the meantime, using the sha256 digest so that we pin to a specific version.
	WolfiDigest = "sha256:7a5b796ae54f72b78b7fc33c8fffee9a363af2c6796dac7c4ef65de8d67d348d" // crane digest ...
	WolfiImage  = "cgr.dev/chainguard/wolfi-base:" + WolfiVersion + "@" + WolfiDigest

	// Also requires us to contact for access. I think that we should.
	// https://hub.docker.com/_/golang/tags
	GolangVersion = "1.22.4"
	GolangDigest  = "sha256:ace6cc3fe58d0c7b12303c57afe6d6724851152df55e08057b43990b927ad5e8" // crane digest ...
	GolangImage   = "golang:" + GolangVersion + "-alpine" + "@" + GolangDigest
)
