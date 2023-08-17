package main

import "context"

func main() {
	dag.Environment().
		WithShell(Demo).
		Serve()
}

func Demo(ctx context.Context) (*Container, error) {
	return dag.Container().From("debian:testing").
		WithExec([]string{"apt", "update"}).
		WithExec([]string{"apt", "install", "-y",
			"build-essential",
			"cmake",
			"doctest-dev",
			"libavdevice-dev",
			"libdeflate-dev",
			"libgpm-dev",
			"libncurses-dev",
			"libqrcodegen-dev",
			"libswscale-dev",
			"libunistring-dev",
			"pandoc",
			"pkg-config",
		}).
		WithMountedDirectory("/src", dag.Git("https://github.com/dankamongmen/notcurses.git").Tag("v3.0.9").Tree()).
		WithWorkdir("/src").
		WithExec([]string{"cmake", "."}).
		WithExec([]string{"make"}).
		WithEntrypoint([]string{"./notcurses-demo"}), nil
}
