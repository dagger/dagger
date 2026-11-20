package build

import (
	"runtime"

	"dagger.io/dagger/core"

	"dagger/engine-dev/consts"
	"dagger/engine-dev/internal/dagger"
	"dagger/engine-dev/internal/dagger/clients/alpine"
)

// alpineTriples are the target triples xx gives Alpine's architectures. xx's
// compilers look for a target's sysroot at /<triple>.
var alpineTriples = map[string]string{
	"amd64": "x86_64-alpine-linux-musl",
	"arm64": "aarch64-alpine-linux-musl",
}

// xxToolchain is an Alpine container that cross-compiles C for the builder's
// platform with xx: tools for the engine's own architecture, xx's scripts, and
// libs, the target's libraries to link, where xx's compilers look for them.
// That is / when the target is the engine's architecture, as xx-apk installs
// them, and the target's sysroot otherwise.
//
// The packages come through the Alpine module, which downloads and unpacks
// each package as its own result, so builds that share a package fetch it
// once. The libraries are unpacked with nothing of theirs run, so nothing
// runs under emulation.
func (build *Builder) xxToolchain(tools, libs []string) *dagger.Container {
	host := runtime.GOARCH
	target := build.platformSpec.Architecture
	sysroot := "/"
	if target != host {
		sysroot = "/" + alpineTriples[target]
	}
	return build.withRerun(alpine.New(alpine.AlpineOpts{
		Branch: consts.AlpineVersion,
		Arch:   host,
		// xx-verify runs file, and would otherwise install it with apk.
		Packages: append([]string{"file"}, tools...),
	}).Container()).
		WithDirectory(sysroot, alpine.New(alpine.AlpineOpts{
			Branch:   consts.AlpineVersion,
			Arch:     target,
			Packages: libs,
		}).Directory()).
		WithDirectory("/", core.NewContainer().From(consts.XxImage).Rootfs()).
		WithEnvVariable("BUILDPLATFORM", "linux/"+host).
		WithEnvVariable("TARGETPLATFORM", string(build.platform))
}
