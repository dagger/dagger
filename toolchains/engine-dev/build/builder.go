package build

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/containerd/platforms"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/engine/distconsts"

	"dagger/engine-dev/consts"
	"dagger/engine-dev/internal/dagger"
)

var dag = dagger.Connect()

type Builder struct {
	source *dagger.Directory

	version string
	tag     string

	platform     dagger.Platform
	platformSpec ocispecs.Platform

	gpuSupport bool

	race bool
}

func NewBuilder(
	ctx context.Context,
	source *dagger.Directory,
	version, tag string,
) (*Builder, error) {
	if version == "" {
		v := dag.Version()
		var err error
		version, err = v.Version(ctx)
		if err != nil {
			return nil, err
		}
		tag, err = v.ImageTag(ctx)
		if err != nil {
			return nil, err
		}
	}
	if tag == "" {
		tag = version
	}
	return &Builder{
		source:       source,
		platform:     dagger.Platform(platforms.DefaultString()),
		platformSpec: platforms.DefaultSpec(),
		version:      version,
		tag:          tag,
	}, nil
}

func (build *Builder) WithRace(race bool) *Builder {
	b := *build
	b.race = race
	return &b
}

func (build *Builder) WithPlatform(p dagger.Platform) *Builder {
	b := *build
	b.platform = p
	b.platformSpec = platforms.Normalize(platforms.MustParse(string(p)))
	return &b
}

func (build *Builder) WithGPUSupport() *Builder {
	b := *build
	b.gpuSupport = true
	return &b
}

func (build *Builder) Engine(ctx context.Context) (*dagger.Container, error) {
	eg, ctx := errgroup.WithContext(ctx)

	sdks := []sdkContentF{build.goSDKContent, build.pythonSDKContent, build.typescriptSDKContent}
	sdkContents := make([]*sdkContent, len(sdks))
	for i, sdk := range sdks {
		eg.Go(func() error {
			content, err := sdk(ctx)
			if err != nil {
				return err
			}
			sdkContents[i] = content
			return nil
		})
	}

	if build.gpuSupport {
		switch build.platformSpec.Architecture {
		case "amd64":
		default:
			return nil, fmt.Errorf("gpu support requires %q arch, not %q", "amd64", build.platformSpec.Architecture)
		}
	}

	pkgs := []string{
		"ca-certificates",
		"posix-libc-utils",
		// for git
		"git", "openssh-client",
		// for decompression
		"pigz", "xz",
		// for CNI
		"iptables", "ip6tables", "dnsmasq",
		// for Kata Containers integration
		"e2fsprogs",
		// for Directory.search
		"ripgrep",
		// for dbs
		"sqlite",
	}
	if build.gpuSupport {
		pkgs = append(pkgs, "nvidia-driver", "nvidia-tools")
	}
	base := dag.
		Wolfi().
		Container(dagger.WolfiContainerOpts{
			Packages: pkgs,
			Arch:     build.platformSpec.Architecture,
		})

	if build.version != "" {
		base = base.WithAnnotation(distconsts.OCIVersionAnnotation, build.version)
	}

	type binAndPath struct {
		path string
		file *dagger.File
	}
	bins := []binAndPath{
		{path: consts.EngineServerPath, file: build.engineBinary(build.race)},
		{path: "/usr/bin/dial-stdio", file: build.dialstdioBinary()},
		{path: "/opt/cni/bin/dnsname", file: build.dnsnameBinary()},
		{path: consts.RuncPath, file: build.runcBin()},
		{path: consts.DaggerInitPath, file: build.daggerInit()},
	}
	for _, bin := range build.qemuBins(ctx) {
		name, err := bin.Name(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get name of binary: %w", err)
		}
		bins = append(bins, binAndPath{path: filepath.Join("/usr/local/bin", name), file: bin})
	}
	for _, bin := range build.cniPlugins() {
		name, err := bin.Name(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get name of binary: %w", err)
		}
		bins = append(bins, binAndPath{path: filepath.Join("/opt/cni/bin", name), file: bin})
	}

	ctr := base
	for _, bin := range bins {
		ctr = ctr.WithFile(bin.path, bin.file)
		eg.Go(func() error {
			return build.verifyPlatform(ctx, bin.file)
		})
	}

	ctr = ctr.
		WithExec([]string{"ln", "-s", "/usr/bin/dial-stdio", "/usr/bin/buildctl"}).
		WithDirectory(distconsts.EngineDefaultStateDir, dag.Directory())

	if err := eg.Wait(); err != nil {
		return nil, err
	}
	for _, content := range sdkContents {
		ctr = ctr.With(content.apply)
	}

	return ctr, nil
}

func (build *Builder) CodegenBinary() *dagger.File {
	return build.binary("./cmd/codegen", false, false, false)
}

func (build *Builder) engineBinary(race bool) *dagger.File {
	return build.binary("./cmd/engine", true, race, true)
}

func (build *Builder) dnsnameBinary() *dagger.File {
	return build.binary("./cmd/dnsname", false, false, false)
}

func (build *Builder) dialstdioBinary() *dagger.File {
	return build.binary("./cmd/dialstdio", false, false, false)
}

func (build *Builder) binary(pkg string, version bool, race bool, cgo bool) *dagger.File {
	return build.Go(version, race, cgo).
		Binary(pkg, dagger.GoBinaryOpts{
			Platform:  build.platform,
			NoSymbols: true,
			NoDwarf:   true,
		})
}

func (build *Builder) Go(version bool, race bool, cgo bool) *dagger.Go {
	var values []string
	if version && build.version != "" {
		values = append(values, "github.com/dagger/dagger/engine.Version="+build.version)
	}
	if version && build.tag != "" {
		values = append(values, "github.com/dagger/dagger/engine.Tag="+build.tag)
	}
	return dag.Go(dagger.GoOpts{
		Source: build.source,
		Values: values,
		Race:   race,
		Cgo:    cgo,
	})
}

func (build *Builder) runcBin() *dagger.File {
	// We build runc from source to enable upgrades to go and other dependencies that
	// can contain CVEs in the builds on github releases
	pkgs := []string{
		"build-base",
		"pkgconf",
		"libseccomp-dev",
		"libseccomp-static",
	}
	var sysroot *dagger.Directory
	if build.platformSpec.Architecture != runtime.GOARCH {
		sysroot = dag.Alpine(dagger.AlpineOpts{
			Arch:     build.platformSpec.Architecture,
			Packages: pkgs,
		}).Container().Rootfs()
		// don't include library deps in host system, it confuses
		// the various compilers
		pkgs = []string{
			"pkgconf",
		}
	}

	src := dag.Git("github.com/opencontainers/runc").Tag(consts.RuncVersion).Tree()
	return dag.Go(dagger.GoOpts{
		Source:        src,
		Cgo:           true,
		Libc:          dagger.GoLibcMusl,
		ExtraPackages: pkgs,
		Values: []string{
			"main.version=" + consts.RuncVersion,
		},
		Ldflags: []string{
			"-linkmode", "external",
			"-extldflags", "'-static'",
		},
		Tags: []string{
			"seccomp",
			"netgo",
			"osusergo",
		},
		Sysroot: sysroot,
	}).Binary(".", dagger.GoBinaryOpts{
		NoSymbols: true,
		NoDwarf:   true,
		Platform:  build.platform,
	})
}

func (build *Builder) qemuBins(ctx context.Context) []*dagger.File {
	dir := dag.
		Container(dagger.ContainerOpts{Platform: build.platform}).
		From(consts.QemuBinImage).
		Rootfs()

	binNames, err := dir.Entries(ctx)
	if err != nil {
		panic(err)
	}

	var bins []*dagger.File
	for _, binName := range binNames {
		bins = append(bins, dir.File(binName))
	}
	return bins
}

func (build *Builder) cniPlugins() (bins []*dagger.File) {
	src := dag.Git("github.com/containernetworking/plugins").Tag(consts.CniVersion).Tree()

	for _, pluginPath := range []string{
		"./plugins/main/bridge",
		"./plugins/main/loopback",
		"./plugins/meta/firewall",
		"./plugins/ipam/host-local",
	} {
		bin := dag.Go(dagger.GoOpts{Source: src}).Binary(pluginPath, dagger.GoBinaryOpts{
			NoSymbols: true,
			NoDwarf:   true,
			Platform:  build.platform,
		})
		bins = append(bins, bin)
	}

	return bins
}

func (build *Builder) daggerInit() *dagger.File {
	return build.binary("./cmd/init", false, false, false)
}

func (build *Builder) goPlatformEnv(ctr *dagger.Container) *dagger.Container {
	ctr = ctr.WithEnvVariable("GOOS", build.platformSpec.OS)
	ctr = ctr.WithEnvVariable("GOARCH", build.platformSpec.Architecture)
	switch build.platformSpec.Architecture {
	case "arm", "arm64":
		switch build.platformSpec.Variant {
		case "", "v8":
		default:
			ctr = ctr.WithEnvVariable("GOARM", strings.TrimPrefix(build.platformSpec.Variant, "v"))
		}
	}
	return ctr
}

// this makes 100% sure that we built the binary for the right platform and didn't, e.g., forget
// to deal with mismatches between the engine host platform and the desired target platform
func (build *Builder) verifyPlatform(ctx context.Context, bin *dagger.File) error {
	name, err := bin.Name(ctx)
	if err != nil {
		return fmt.Errorf("failed to get name of binary: %w", err)
	}
	mntPath := filepath.Join("/mnt", name)
	out, err := dag.
		Alpine(dagger.AlpineOpts{
			Branch:   consts.AlpineVersion,
			Packages: []string{"file"},
		}).
		Container().
		WithMountedFile(mntPath, bin).
		WithExec([]string{"file", mntPath}).
		Stdout(ctx)
	if err != nil {
		return fmt.Errorf("failed to call file on binary %s: %w", name, err)
	}
	if !strings.Contains(out, platformToFileArch[build.platformSpec.Architecture]) {
		return fmt.Errorf("binary %s is not for %s", name, build.platformSpec.Architecture)
	}
	return nil
}

var platformToFileArch = map[string]string{
	"amd64": "x86-64",
	"arm64": "aarch64",
}
