package main

import "dagger.io/dagger"

// GlobalCache sets $GOMODCACHE to /go/pkg/mod and $GOCACHE to /go/build-cache
// and mounts cache volumes to both.
//
// The cache volumes are named "go-mod" and "go-build" respectively.
func GlobalCache(ctr *dagger.Container) *dagger.Container {
	return ctr.
		WithMountedCache("/go/pkg/mod", dagger.DefaultClient().CacheVolume("go-mod")).
		WithEnvVariable("GOMODCACHE", "/go/pkg/mod").
		WithMountedCache("/go/build-cache", dagger.DefaultClient().CacheVolume("go-build")).
		WithEnvVariable("GOCACHE", "/go/build-cache")
}

// BinPath sets $GOBIN to /go/bin and prepends it to $PATH.
func BinPath(ctr *dagger.Container) *dagger.Container {
	return ctr.
		WithEnvVariable("GOBIN", "/go/bin").
		WithEnvVariable("PATH", "$GOBIN:$PATH", dagger.ContainerWithEnvVariableOpts{
			Expand: true,
		})
}

func Cd(dst string, src *dagger.Directory) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.
			WithMountedDirectory(dst, src).
			WithWorkdir(dst)
	}
}
