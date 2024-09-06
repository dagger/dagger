package main

import (
	"dagger/runc/internal/dagger"
	"runtime"

	"github.com/dagger/dagger/engine/distconsts"
)

func New(
	// Version of runc to build
	// +optional
	// +default="v1.1.12"
	version string,
	// Platform to build for
	// +optional
	platform dagger.Platform,
) Runc {
	return Runc{
		Version:  version,
		Platform: platform,
	}
}

type Runc struct {
	Version  string          // +private
	Platform dagger.Platform // +private
}

// Build the runc at the specified version and platform
func (runc Runc) Binary() *dagger.File {
	// We build runc from source to enable upgrades to go and other dependencies that
	// can contain CVEs in the builds on github releases
	src := dag.Git("github.com/opencontainers/runc").Tag(runc.Version).Tree()
	// Custom base image for special cross-platform build magic
	base := dag.Container().
		From(distconsts.GolangImage).
		WithEnvVariable("BUILDPLATFORM", "linux/"+runtime.GOARCH).
		WithEnvVariable("TARGETPLATFORM", string(runc.Platform)).
		WithExec([]string{"apk", "add", "clang", "lld", "git", "pkgconf"}).
		WithDirectory("/", dag.Container().From("tonistiigi/xx:1.2.1").Rootfs()).
		WithExec([]string{"xx-apk", "update"}).
		WithExec([]string{"xx-apk", "add", "build-base", "pkgconf", "libseccomp-dev", "libseccomp-static"}).
		WithDirectory("/src", dag.Directory()).
		WithWorkdir("/src")
	// Setup a standard go environment on top of our custom base
	env := dag.
		Go(src, dagger.GoOpts{
			Cgo:  true,
			Base: base,
		}).
		Env(dagger.GoEnvOpts{
			Platform: runc.Platform,
		})
	// Execute custom go build commands
	return env.
		// TODO: runc v1.1.x uses an old version of golang.org/x/net, which has a CVE:
		// https://github.com/advisories/GHSA-4374-p667-p6c8
		// We upgrade it here to avoid that showing up in our image scans. This can be removed
		// once runc has released a new minor version and we upgrade to it (the go.mod in runc
		// main branch already has the updated version).
		WithExec([]string{"go", "get", "golang.org/x/net@v0.25.0"}).
		WithExec([]string{"go", "mod", "tidy"}).
		WithExec([]string{"go", "mod", "vendor"}).
		WithExec([]string{
			"xx-go", "build",
			"-trimpath",
			"-buildmode=pie",
			"-tags", "seccomp netgo osusergo",
			"-ldflags", "-X main.version=" + runc.Version + " -linkmode external -extldflags -static-pie",
			"-o", "runc",
			".",
		}).
		File("runc")
}
