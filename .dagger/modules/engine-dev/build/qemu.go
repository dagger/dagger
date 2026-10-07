package build

import (
	_ "embed"
	"strings"

	"dagger/engine-dev/consts"
	"dagger/engine-dev/internal/dagger"
)

//go:embed qemu/run-host-binaries-directly.patch
var qemuRunHostBinariesPatch string

// qemuGuestArchs are the QEMU guest architectures the engine can emulate.
// The engine's own architecture is skipped since it runs natively.
var qemuGuestArchs = []string{"x86_64", "aarch64", "arm", "i386", "riscv64", "s390x", "ppc64le"}

var platformToQemuArch = map[string]string{
	"amd64": "x86_64",
	"arm64": "aarch64",
}

// qemuBins returns the names of the bundled QEMU user-mode emulators and the
// directory containing them. The names are known up front so callers don't
// have to wait for the build to finish before wiring the files elsewhere.
func (build *Builder) qemuBins() (*dagger.Directory, []string) {
	var targets, names []string
	for _, arch := range qemuGuestArchs {
		if arch == platformToQemuArch[build.platformSpec.Architecture] {
			continue
		}
		targets = append(targets, arch+"-linux-user")
		names = append(names, "buildkit-qemu-"+arch)
	}

	src := dag.HTTP(
		"https://download.qemu.org/qemu-"+consts.QemuVersion+".tar.xz",
		dagger.HTTPOpts{Checksum: consts.QemuChecksum},
	)
	// tonistiigi/binfmt's patches make execve in an emulated process re-run
	// QEMU on the new program, so emulation works without binfmt_misc. Ours
	// on top runs host binaries directly instead of emulating them.
	binfmtPatches := dag.Git("https://github.com/tonistiigi/binfmt").
		Commit(consts.BinfmtCommit).
		Tree().
		Directory("patches")
	patches := dag.Directory().
		WithDirectory("0-cpu-max-arm", binfmtPatches.Directory("cpu-max-arm")).
		WithDirectory("1-meson", binfmtPatches.Directory("meson")).
		WithDirectory("2-direct-execve", binfmtPatches.Directory("buildkit-direct-execve-v"+consts.QemuPatchSeries)).
		WithNewFile("3-dagger/run-host-binaries-directly.patch", qemuRunHostBinariesPatch)

	// Cross-compile on the build platform with xx rather than building under
	// emulation for other target platforms.
	return dag.Container().
		From(consts.AlpineImage).
		WithExec([]string{"apk", "add", "--no-cache",
			"clang", "lld", "llvm", "make", "ninja", "pkgconf", "python3", "perl", "bash", "patch", "xz",
		}).
		WithDirectory("/", dag.Container().From(consts.XxImage).Rootfs()).
		WithEnvVariable("TARGETPLATFORM", string(build.platform)).
		WithExec([]string{"xx-apk", "add", "--no-cache",
			"musl-dev", "gcc", "glib-dev", "glib-static", "linux-headers", "pcre2-dev", "pcre2-static", "zlib-static",
		}).
		WithMountedFile("/qemu.tar.xz", src).
		WithMountedDirectory("/patches", patches).
		WithEnvVariable("QEMU_TARGETS", strings.Join(targets, " ")).
		WithExec([]string{"sh", "-euc", `
mkdir /src /out
tar -xJf /qemu.tar.xz -C /src --strip-components=1 --exclude='*/roms'
cd /src
for p in /patches/*/*.patch; do patch -s -p1 < "$p"; done
mkdir build && cd build
../configure \
  --prefix=/usr \
  --without-default-features \
  --enable-linux-user \
  --disable-system \
  --static \
  --disable-debug-info \
  --disable-werror \
  --cross-prefix="$(xx-info)-" \
  --host-cc="$(TARGETPLATFORM= xx-clang --print-target-triple)-clang" \
  --cc="$(xx-clang --print-target-triple)-clang" \
  --extra-ldflags=-latomic \
  --target-list="$QEMU_TARGETS"
make -j"$(nproc)"
for t in $QEMU_TARGETS; do
  arch="${t%-linux-user}"
  llvm-strip -o "/out/buildkit-qemu-$arch" "qemu-$arch"
  xx-verify --static "/out/buildkit-qemu-$arch"
done
`}).
		Directory("/out"), names
}
