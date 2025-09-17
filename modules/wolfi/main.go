// A Dagger Module to integrate with Wolfi Linux
//
// Wolfi is a container-native Linux distribution with an emphasis on security.
// https://wolfi.dev
package main

import (
	"fmt"
	"strings"

	"github.com/dagger/dagger/modules/wolfi/internal/dagger"
)

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
	// Overlay images to merge on top of the base.
	// See https://twitter.com/ibuildthecloud/status/1721306361999597884
	// +optional
	overlays []*dagger.Container,
) *dagger.Container {
	config := dag.Alpine(dagger.AlpineOpts{
		Distro:   dagger.AlpineDistroWolfi,
		Packages: packages,
		Arch:     arch,
	})
	ctr := config.Container()
	for _, overlay := range overlays {
		ctr = ctr.WithDirectory("/", overlay.Rootfs()) // DONT COMMIT, why is this in a loop rather than simply using the last overlay?
	}

	hasGo := false
	for _, x := range packages {
		if strings.HasPrefix(x, "go~") {
			hasGo = true
		}
	}
	if hasGo {
		fmt.Printf("should have go\n")
		//out, err := ctr.WithExec([]string{"sh", "-c", "echo ACB here I am && env && ls -la /usr/sbin && which go"}).Stdout(context.TODO())
		//if err != nil {
		//	panic(fmt.Sprintf("ACB failed in wolfi.Container with %d overlays err=%v", len(overlays), err))
		//}
		//fmt.Printf("ACB in wolf worked with go %s\n", out)
	}

	return ctr
}
