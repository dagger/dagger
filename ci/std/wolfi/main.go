// A Dagger Module to integrate with Wolfi Linux
//
// Wolfi is a container-native Linux distribution with an emphasis on security.
// https://wolfi.dev
package main

// A Wolfi Linux configuration
type Wolfi struct{}

// Build a Wolfi Linux container
func (w *Wolfi) Container(
	// APK packages to install
	// +optional
	packages []string,
	// Overlay images to merge on top of the base.
	// See https://twitter.com/ibuildthecloud/status/1721306361999597884
	// +optional
	overlays []*Container,
) *Container {
	ctr := dag.Apko().Wolfi(packages)
	for _, overlay := range overlays {
		ctr = ctr.WithDirectory("/", overlay.Rootfs())
	}
	return ctr
}
