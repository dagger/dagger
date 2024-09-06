package main

import (
	"dagger/qemu/internal/dagger"
)

func New(
	// Container image to download the qemu binaries from
	// +optional
	// +default="tonistiigi/binfmt@sha256:e06789462ac7e2e096b53bfd9e607412426850227afeb1d0f5dfa48a731e0ba5"
	// FIXME: make this a container type, once API supports platform selection
	image string,
	// Platform to target
	// +optional
	platform dagger.Platform,
	// Pattern to filter binaries
	// +optional
	// +default="buildkit-qemu-*"
	pattern string,
) Qemu {
	return Qemu{
		Image:    image,
		Platform: platform,
		Pattern:  pattern,
	}
}

type Qemu struct {
	Image    string          // +private FIXME make this a container type, once API supports platform selection
	Platform dagger.Platform // +private
	Pattern  string          // +private
}

func (qemu *Qemu) Binaries() *dagger.Directory {
	rootfs := dag.
		Container(dagger.ContainerOpts{Platform: qemu.Platform}).
		From(qemu.Image).
		Rootfs()
	return dag.Directory().
		WithDirectory("", rootfs, dagger.DirectoryWithDirectoryOpts{
			Include: []string{qemu.Pattern},
		})
}
