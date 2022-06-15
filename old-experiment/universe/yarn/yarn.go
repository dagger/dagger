package yarn

import (
	"dagger.io/dagger"
	"dagger.io/universe/docker"
)

func Run(source *dagger.FS, arg string) *dagger.FS {
	return docker.
		Build().
		Pull("alpine").
		Copy(source, "/", "/src").
		Run("yarn", "install", "--production", "false").
		Run("yarn", "run", arg).
		FS()
}
