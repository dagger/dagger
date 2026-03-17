// A Dagger Module to integrate with Wolfi Linux
//
// Wolfi is a container-native Linux distribution with an emphasis on security.
// https://wolfi.dev
package main

import "github.com/dagger/dagger/modules/wolfi/internal/dagger"

// A Wolfi Linux configuration
type Wolfi struct{}

// Build a Wolfi Linux container
func (w *Wolfi) Container(
	// APK packages to install
	// +optional
	packages []string,
	// Hardware architecture to target
	// +optional
	arch string,
	// Extra repositories to add to the package resolver
	// +optional
	extraRepositories []string,
	// Extra keys needed to authenticate the extra repositories
	// +optional
	extraKeyURLs []string,
	// Overlay images to merge on top of the base.
	// See https://twitter.com/ibuildthecloud/status/1721306361999597884
	// +optional
	overlays []*dagger.Container,
) *dagger.Container {
	config := dag.Alpine(dagger.AlpineOpts{
		Distro:            dagger.AlpineDistroWolfi,
		Packages:          packages,
		Arch:              arch,
		ExtraRepositories: extraRepositories,
		ExtraKeyUrls:      extraKeyURLs,
	})
	ctr := config.Container()
	for _, overlay := range overlays {
		ctr = ctr.WithDirectory("/", overlay.Rootfs())
	}
	return ctr
}
