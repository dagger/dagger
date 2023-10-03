package main

// Vito says hi.
type Vito struct{}

// HelloWorld says hi.
func (m *Vito) HelloWorld() string {
	return "hey"
}

func (m *Vito) YamlInvaders() *Container {
	// repo := dag.Git("https://github.com/grouville/ascii-invaders.git").Branch("no-yaml-parsing").Tree()
	repo := dag.Git("https://github.com/grouville/ascii-invaders.git").Branch("with-yaml-parsing").Tree()

	return dag.Container().From("debian:buster").
		WithExec([]string{"apt", "update"}).
		WithExec([]string{"apt", "install", "-y", "build-essential", "libncursesw5-dev", "libyaml-dev"}).
		WithMountedDirectory("/src", repo).
		WithWorkdir("/src").
		WithExec([]string{"make"}).
		WithExec([]string{"./ascii_invaders"})
}
