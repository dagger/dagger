package docker

import "dagger.io/dagger"

func Build() *Docker {
	return &Docker{}
}

type Docker struct {
}

func (d *Docker) Pull(string) *Docker {
	return d
}

func (d *Docker) Run(...string) *Docker {
	return d
}

func (d *Docker) Copy(*dagger.FS, string, string) *Docker {
	return d
}

func (d *Docker) FS() *dagger.FS {
	return dagger.Scratch()
}
