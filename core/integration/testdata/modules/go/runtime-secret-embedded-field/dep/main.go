package main

import "dagger/dep/internal/dagger"

type Dep struct{}

type SecretMount struct {
	Secret *dagger.Secret
	Path   string
}

func (m *Dep) SecretMount(path string) *SecretMount {
	return &SecretMount{
		Secret: dag.SetSecret("foo", "hello from foo"),
		Path:   path,
	}
}

func (m *SecretMount) Mount(ctr *dagger.Container) *dagger.Container {
	return ctr.WithMountedSecret(m.Path, m.Secret)
}
