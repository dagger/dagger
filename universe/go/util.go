package main

// GlobalCache sets $GOMODCACHE to /go/pkg/mod and $GOCACHE to /go/build-cache
// and mounts cache volumes to both.
//
// The cache volumes are named "go-mod" and "go-build" respectively.
func GlobalCache(ctr *Container) *Container {
	return ctr.
		WithMountedCache("/go/pkg/mod", dag.CacheVolume("go-mod")).
		WithEnvVariable("GOMODCACHE", "/go/pkg/mod").
		WithMountedCache("/go/build-cache", dag.CacheVolume("go-build")).
		WithEnvVariable("GOCACHE", "/go/build-cache")
}

// BinPath sets $GOBIN to /go/bin and prepends it to $PATH.
func BinPath(ctr *Container) *Container {
	return ctr.
		WithEnvVariable("GOBIN", "/go/bin").
		WithEnvVariable("PATH", "$GOBIN:$PATH", ContainerWithEnvVariableOpts{
			Expand: true,
		})
}

func Cd(dst string, src *Directory) WithContainerFunc {
	return func(ctr *Container) *Container {
		return ctr.
			WithMountedDirectory(dst, src).
			WithWorkdir(dst)
	}
}
