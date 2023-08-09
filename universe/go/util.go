package main

import "dagger.io/dagger"

// TODO: integrate this into the API, have it cd into /absddfksdf so it doesn't
// have to take an arg?
func Cd(dst string, src *dagger.Directory) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.
			WithMountedDirectory(dst, src).
			WithWorkdir(dst)
	}
}

func GlobalCache(ctx dagger.Context) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.
			WithMountedCache("/go/pkg/mod", ctx.Client().CacheVolume("go-mod")).
			WithEnvVariable("GOMODCACHE", "/go/pkg/mod").
			WithMountedCache("/go/build-cache", ctx.Client().CacheVolume("go-build")).
			WithEnvVariable("GOCACHE", "/go/build-cache")
	}
}

func BinPath(ctr *dagger.Container) *dagger.Container {
	return ctr.
		WithEnvVariable("GOBIN", "/go/bin").
		WithEnvVariable("PATH", "$GOBIN:$PATH", dagger.ContainerWithEnvVariableOpts{
			Expand: true,
		})
}
