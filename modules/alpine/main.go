// A Dagger Module to integrate with Alpine Linux

package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	goapk "chainguard.dev/apko/pkg/apk/apk"
	"github.com/dagger/dagger/dev/alpine/internal/dagger"
	"golang.org/x/mod/semver"
	"golang.org/x/sync/errgroup"
)

type Distro string

// FIXME: these names stutter - but "alpine" clashes with the name of the module
const (
	DistroAlpine Distro = "DISTRO_ALPINE"
	DistroWolfi  Distro = "DISTRO_WOLFI"
)

func New(
	// Hardware architecture to build for
	// +optional
	arch string,
	// Alpine branch to download packages from
	// +optional
	// +default="edge"
	branch string,
	// APK packages to install
	// +optional
	packages []string,

	// Alpine distribution to use
	// +optional
	// +default="DISTRO_ALPINE"
	distro Distro,
) (Alpine, error) {
	if arch == "" {
		arch = runtime.GOARCH
	}
	goArch := arch
	if apkArch := goapk.ArchToAPK(arch); apkArch != arch {
		arch = apkArch
	}

	switch {
	case branch == "edge":
	case semver.IsValid("v" + branch):
		branch = "v" + branch
		fallthrough
	case semver.IsValid(branch):
		// discard anything after major.minor (that's how alpine branches are named)
		branch = semver.MajorMinor(branch)
	default:
		return Alpine{}, fmt.Errorf("invalid branch %s", branch)
	}

	return Alpine{
		Distro:   distro,
		Branch:   branch,
		Arch:     arch,
		Packages: packages,

		GoArch: goArch,
	}, nil
}

// An Alpine Linux configuration
type Alpine struct {
	// The distro to use
	Distro Distro
	// The hardware architecture to build for
	Arch string
	// The Alpine branch to download packages from
	Branch string
	// The APK packages to install
	Packages []string

	// the GOARCH equivalent of Arch
	// +private
	GoArch string
}

// Build an Alpine Linux container
func (m *Alpine) Container(ctx context.Context) (*dagger.Container, error) {
	var branch *goapk.ReleaseBranch
	var repos []string
	var basePkgs []string

	switch m.Distro {
	case DistroAlpine:
		releases, err := alpineReleases()
		if err != nil {
			return nil, fmt.Errorf("failed to get alpine releases: %w", err)
		}
		branch = releases.GetReleaseBranch(m.Branch)
		if branch == nil {
			return nil, fmt.Errorf("failed to get alpine release %q", m.Branch)
		}
		repos = alpineRepositories(*branch)

		basePkgs = []string{"alpine-baselayout", "alpine-release", "busybox", "apk-tools"}
	case DistroWolfi:
		releases := wolfiReleases()
		if m.Branch != "edge" {
			return nil, fmt.Errorf("failed to get wolfi release %q", m.Branch)
		}
		branch = releases.GetReleaseBranch("main")
		if branch == nil {
			return nil, fmt.Errorf("failed to get wolfi release %q", m.Branch)
		}
		repos = wolfiRepositories()

		basePkgs = []string{"wolfi-baselayout", "busybox", "apk-tools"}
	default:
		return nil, fmt.Errorf("unknown distro %q", m.Distro)
	}

	keys, err := fetchKeys(*branch, m.Arch)
	if err != nil {
		return nil, fmt.Errorf("failed to get keys: %w", err)
	}
	indexes, err := goapk.GetRepositoryIndexes(ctx, repos, keys, m.Arch, goapk.WithHTTPClient(http.DefaultClient))
	if err != nil {
		return nil, fmt.Errorf("failed to get indexes: %w", err)
	}
	pkgResolver := goapk.NewPkgResolver(ctx, indexes)

	ctr := dag.Container(dagger.ContainerOpts{Platform: dagger.Platform("linux/" + m.GoArch)})
	ctr, err = m.withPkgs(ctx, ctr, pkgResolver, basePkgs)
	if err != nil {
		return nil, fmt.Errorf("failed to create base container: %w", err)
	}
	ctr, err = m.withPkgs(ctx, ctr, pkgResolver, m.Packages)
	if err != nil {
		return nil, fmt.Errorf("failed to create package container: %w", err)
	}

	// NOTE: "apk add" will not work in this container. Generally this is a good
	// thing since it's more efficient to install all packages here and keep
	// the output container immutable.
	//
	// However, if the need to support that arises the following would be needed:
	// * /etc/apk/arch with the architecture written
	// * /etc/apk/repositories with the list of repo urls
	// * /etc/apk/world with each top level installed package name written
	// * /etc/apk/keys/ with a file for each key
	// * /lib/apk/db/installed with package metadata for each installed pkg
	//   * the goapk.PackageToInstalled + AddInstalledPackage code helps here

	ctr = ctr.WithEnvVariable("PATH", strings.Join([]string{
		"/usr/local/sbin",
		"/usr/local/bin",
		"/usr/sbin",
		"/usr/bin",
		"/sbin",
		"/bin",
	}, ":"))

	return ctr, nil
}

func (m *Alpine) withPkgs(
	ctx context.Context,
	ctr *dagger.Container,
	pkgResolver *goapk.PkgResolver,
	pkgs []string,
) (*dagger.Container, error) {
	repoPkgs, conflicts, err := pkgResolver.GetPackagesWithDependencies(ctx, pkgs, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get packages: %w", err)
	}
	if len(conflicts) > 0 {
		// TODO: confirm that ignoring also matches apk add behavior (seems like it does)
		fmt.Printf("package conflicts: %v\n", conflicts)
	}

	setupBase := dag.Container().From("busybox:latest")

	type apkPkg struct {
		name        string
		dir         *dagger.Directory
		preInstall  *dagger.File
		postInstall *dagger.File
		trigger     *dagger.File
		rmFileNames []string
	}

	var eg errgroup.Group
	alpinePkgs := make([]*apkPkg, len(repoPkgs))
	for i, pkg := range repoPkgs {
		eg.Go(func() error {
			url := pkg.URL()
			mntPath := filepath.Join("/mnt", filepath.Base(url))
			outDir := "/out"

			unpacked := setupBase.
				WithMountedFile(mntPath, dag.HTTP(url)).
				WithMountedDirectory(outDir, dag.Directory()).
				WithWorkdir(outDir).
				WithExec([]string{"tar", "-xf", mntPath})

			alpinePkg := &apkPkg{
				name: pkg.PackageName(),
				dir:  unpacked.Directory(outDir),
			}

			entries, err := alpinePkg.dir.Entries(ctx)
			if err != nil {
				return fmt.Errorf("failed to get alpine package entries: %w", err)
			}

			// HACK: /lib64 is a link, so don't overwrite it
			// - wolfi-baselayout links /lib64 -> /lib
			// - ld-linux installs to /lib64
			// TODO: this should *probably* apply to /usr/lib64/ and
			// /usr/local/lib64/ as well
			if m.Distro == DistroWolfi && pkg.PackageName() != "wolfi-baselayout" && slices.Contains(entries, "lib64") {
				alpinePkg.dir = alpinePkg.dir.
					WithDirectory("/lib", alpinePkg.dir.Directory("/lib64")).
					WithoutDirectory("/lib64")
			}

			for _, entry := range entries {
				if !strings.HasPrefix(entry, ".") {
					continue
				}
				alpinePkg.rmFileNames = append(alpinePkg.rmFileNames, entry)
				switch entry {
				case ".pre-install":
					alpinePkg.preInstall = unpacked.File(filepath.Join(outDir, entry))
				case ".post-install":
					alpinePkg.postInstall = unpacked.File(filepath.Join(outDir, entry))
				case ".trigger":
					alpinePkg.trigger = unpacked.File(filepath.Join(outDir, entry))
				}
			}

			alpinePkgs[i] = alpinePkg
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, fmt.Errorf("failed to get alpine packages: %w", err)
	}

	for _, pkg := range alpinePkgs {
		ctr = ctr.With(pkgscript("pre-install", pkg.name, pkg.preInstall))
		ctr = ctr.WithDirectory("/", pkg.dir, dagger.ContainerWithDirectoryOpts{
			Exclude: pkg.rmFileNames,
		})
		ctr = ctr.With(pkgscript("post-install", pkg.name, pkg.postInstall))
	}
	for _, pkg := range alpinePkgs {
		ctr = ctr.With(pkgscript("trigger", pkg.name, pkg.trigger))
	}

	return ctr, nil
}

func fetchKeys(branch goapk.ReleaseBranch, arch string) (map[string][]byte, error) {
	urls := branch.KeysFor(arch, time.Now())
	keys := make(map[string][]byte)
	for _, u := range urls {
		res, err := http.Get(u)
		if err != nil {
			return nil, fmt.Errorf("failed to get alpine key at %s: %w", u, err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("unable to get alpine key at %s: %v", u, res.Status)
		}
		keyBytes, err := io.ReadAll(res.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read alpine key at %s: %w", u, err)
		}
		keys[filepath.Base(u)] = keyBytes
	}
	return keys, nil
}

func pkgscript(kind string, pkg string, script *dagger.File) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		if script == nil {
			return ctr
		}

		path := fmt.Sprintf("/tmp/%s.%s", pkg, kind)
		return ctr.
			WithMountedFile(path, script).
			WithExec([]string{path}).
			WithoutMount(path)
	}
}
