// A Dagger Module to integrate with Alpine Linux

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	goapk "chainguard.dev/apko/pkg/apk/apk"
	"github.com/dagger/dagger/dev/alpine/internal/dagger"
	"golang.org/x/mod/semver"
	"golang.org/x/sync/errgroup"
)

const (
	alpineRepository  = "https://dl-cdn.alpinelinux.org/alpine"
	alpineReleasesURL = "https://alpinelinux.org/releases.json"
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
		Branch:   branch,
		Arch:     arch,
		Packages: packages,

		GoArch: goArch,
	}, nil
}

// An Alpine Linux configuration
type Alpine struct {
	// The hardware architecture to build for
	Arch string
	// The Alpine branch to download packages from
	Branch string
	// The APK packages to install
	Packages []string

	// the GOARCH equivalent of Arch
	//+private
	GoArch string
}

// Build an Alpine Linux container
func (m *Alpine) Container(ctx context.Context) (*dagger.Container, error) {
	keys, err := m.keys()
	if err != nil {
		return nil, fmt.Errorf("failed to get alpine keys: %w", err)
	}

	mainRepo := goapk.NewRepositoryFromComponents(
		alpineRepository,
		m.Branch,
		"main",
		m.Arch,
	)
	communityRepo := goapk.NewRepositoryFromComponents(
		alpineRepository,
		m.Branch,
		"community",
		m.Arch,
	)

	indexes, err := goapk.GetRepositoryIndexes(ctx, []string{mainRepo.URI, communityRepo.URI}, keys, "", goapk.WithHTTPClient(http.DefaultClient))
	if err != nil {
		return nil, fmt.Errorf("failed to get alpine main indexes: %w", err)
	}

	pkgResolver := goapk.NewPkgResolver(ctx, indexes)

	pkgs := append([]string{"alpine-baselayout", "alpine-release", "busybox"}, m.Packages...)

	repoPkgs, conflicts, err := pkgResolver.GetPackagesWithDependencies(ctx, pkgs, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get alpine packages: %w", err)
	}
	if len(conflicts) > 0 {
		// TODO: confirm that ignoring also matches apk add behavior (seems like it does)
		fmt.Printf("alpine package conflicts: %v\n", conflicts)
	}

	setupBase := dag.Container().From("busybox:latest")

	type alpinePackage struct {
		dir         *dagger.Directory
		preInstall  *dagger.File
		postInstall *dagger.File
		trigger     *dagger.File
		rmFileNames []string
	}

	var eg errgroup.Group
	alpinePkgs := make([]*alpinePackage, len(repoPkgs))
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

			alpinePkg := &alpinePackage{
				dir: unpacked.Directory(outDir),
			}

			entries, err := alpinePkg.dir.Entries(ctx)
			if err != nil {
				return fmt.Errorf("failed to get alpine package entries: %w", err)
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

	ctr := dag.Container(dagger.ContainerOpts{Platform: dagger.Platform(m.GoArch)})
	for _, pkg := range alpinePkgs {
		if pkg.preInstall != nil {
			ctr = ctr.
				WithMountedFile("/tmp/script", pkg.preInstall).
				WithExec([]string{"/tmp/script"}).
				WithoutMount("/tmp/script")
		}
		ctr = ctr.WithDirectory("/", pkg.dir, dagger.ContainerWithDirectoryOpts{
			Exclude: pkg.rmFileNames,
		})
		if pkg.postInstall != nil {
			ctr = ctr.
				WithMountedFile("/tmp/script", pkg.postInstall).
				WithExec([]string{"/tmp/script"}).
				WithoutMount("/tmp/script")
		}
	}
	for _, pkg := range alpinePkgs {
		if pkg.trigger != nil {
			ctr = ctr.
				WithMountedFile("/tmp/script", pkg.trigger).
				WithExec([]string{"/tmp/script"}).
				WithoutMount("/tmp/script")
		}
	}

	return ctr, nil
}

func (m *Alpine) keys() (map[string][]byte, error) {
	releases, err := m.releases()
	if err != nil {
		return nil, fmt.Errorf("failed to get alpine releases: %w", err)
	}
	branch := releases.GetReleaseBranch(m.Branch)
	if branch == nil {
		return nil, fmt.Errorf("failed to get alpine branch for version %s", m.Branch)
	}
	urls := branch.KeysFor(m.Arch, time.Now())

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

func (m *Alpine) releases() (*goapk.Releases, error) {
	res, err := http.Get(alpineReleasesURL)
	if err != nil {
		return nil, fmt.Errorf("failed to get alpine releases: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unable to get alpine releases at %s: %v", alpineReleasesURL, res.Status)
	}
	b, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read alpine releases: %w", err)
	}
	var releases goapk.Releases
	if err := json.Unmarshal(b, &releases); err != nil {
		return nil, fmt.Errorf("failed to unmarshal alpine releases: %w", err)
	}

	return &releases, nil
}
