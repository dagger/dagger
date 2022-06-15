package alpine

import (
	"dagger.io/universe/docker"
)

type Alpine struct {
	image *docker.Docker
}

func New() *Alpine {
	return &Alpine{
		image: docker.Build().Pull("alpine"),
	}
}

func (a *Alpine) Image() *docker.Docker {
	return a.image
}

func (a *Alpine) Add(pkg ...string) *Alpine {
	for _, p := range pkg {
		a.image = a.image.Run("apk", "add", p)
	}

	return a
}
