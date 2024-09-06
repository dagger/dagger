// A Dagger Module to integrate with Alpine Linux

package main

import (
	"encoding/json"
	"fmt"
	"runtime"
	"time"

	"github.com/dagger/dagger/dev/alpine/internal/dagger"
	"golang.org/x/mod/semver"
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
}

// Build an Alpine Linux container
func (m *Alpine) Container() *dagger.Container {
	return dag.
		Container(dagger.ContainerOpts{
			Platform: dagger.Platform("linux/" + m.Arch),
		}).
		Import(m.Archive())
}

// Build an Alpine Linux OCI archive
func (m *Alpine) Archive() *dagger.File {
	return dag.
		Container().
		From("cgr.dev/chainguard/apko").
		WithMountedCache("/apkache", dagger.Connect().CacheVolume("apko")).
		WithFile("apko.yml", m.ApkoConfig()).
		// HACK: wrapping the apko build with this time based env ensures that
		// the cache is invalidated once-per day. Without this, we can't ensure
		// that we get the newest versions.
		WithEnvVariable("DAGGER_APK_CACHE_BUSTER", fmt.Sprintf("%d", time.Now().Truncate(24*time.Hour).Unix())).
		WithExec([]string{
			"apko", "build",
			"--cache-dir", "/apkache/",
			"apko.yml",
			"latest",
			"img.tar",
		}).
		File("img.tar")
}

// Generate the APKO build configuration
func (m Alpine) ApkoConfig() *dagger.File {
	configJSON, _ := json.Marshal(m.apkoConfig())
	return dag.
		Directory().
		WithNewFile("apko.yml", string(configJSON)).
		File("apko.yml")
}

func (m Alpine) apkoConfig() APKOConfig {
	var config APKOConfig
	config.Contents.Packages = append([]string{"alpine-base"}, m.Packages...)
	for _, path := range []string{"main", "community"} {
		repo := "https://dl-cdn.alpinelinux.org/alpine/" + m.Branch + "/" + path
		config.Contents.Repositories = append(config.Contents.Repositories, repo)
	}
	config.Archs = []string{m.Arch}
	return config
}

type APKOConfig struct {
	Contents APKOConfigContents `json:"contents"`
	Archs    []string           `json:"archs"`
}

type APKOConfigContents struct {
	Repositories []string `json:"repositories"`
	Packages     []string `json:"packages"`
}
