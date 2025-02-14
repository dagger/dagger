package main

import (
	"dagger/img/internal/dagger"
)

func New(
	// +optional
	// +defaultPath="snippets"
	snippets *dagger.Directory,
) Img {
	return Img{
		Snippets: snippets, //+private
	}
}

type Img struct {
	Snippets *dagger.Directory
}

func (m *Img) Recordings() *dagger.Directory {
	return dag.Directory().
		WithFile(
			"build.gif",
			dag.Recorder(dagger.RecorderOpts{Werkdir: m.Snippets.Directory("programmable-pipelines-1/go")}).
				Exec("cat main.go").
				Exec("dagger call build --src=https://github.com/golang/example#master:/hello --arch=amd64 --os=linux").
				Gif())
		WithFile(
			"build-publish.gif",
			dag.Recorder(dagger.RecorderOpts{Werkdir: m.Snippets.Directory("programmable-pipelines-1/go")}).
				Exec("dagger call build --src=https://github.com/golang/example#master:/hello --arch=amd64 --os=linux publish --address=ttl.sh/my-img").
				Gif()).
		WithFile(
			"build-export.gif",
			dag.Recorder(dagger.RecorderOpts{Werkdir: m.Snippets.Directory("programmable-pipelines-2/go")}).
				Exec("cat main.go").
				Exec("dagger call build --src=https://github.com/golang/example#master:/hello --arch=amd64 --os=linux export --path=/tmp/out").
				Gif())
	*/
}
