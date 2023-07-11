package universe

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
