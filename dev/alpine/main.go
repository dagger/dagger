// A Dagger Module to integrate with Alpine Linux

package main

import (
	"encoding/json"
	"runtime"

	"github.com/dagger/dagger/dev/alpine/internal/dagger"
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
) Alpine {
	if arch == "" {
		arch = runtime.GOARCH
	}
	return Alpine{
		Branch:   branch,
		Arch:     arch,
		Packages: packages,
	}
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
