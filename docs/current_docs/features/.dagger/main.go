// A dagger module to manage the "features" section of the Dagger docs

package main

import (
	"dagger/features/internal/dagger"
)

func New(
	// +optional
	// +defaultPath="snippets"
	snippets *dagger.Directory,
) Features {
	return Features{
		Snippets: snippets, //+private
	}
}

type Features struct {
	Snippets *dagger.Directory
}

// Generate demo recordings as gifs to be embedded in the docs
func (m Features) Recordings() *dagger.Directory {
	return dag.Directory().
		WithFile(
			"build.gif",
			dag.Recorder(dagger.RecorderOpts{Workdir: m.Snippets.Directory("programmable-pipelines-1/go")}).
				Exec("cat main.go").
				Exec("dagger call build --src=https://github.com/golang/example#master:/hello --arch=amd64 --os=linux").
				Gif()).
		WithFile(
			"build-publish.gif",
			dag.Recorder(dagger.RecorderOpts{Workdir: m.Snippets.Directory("programmable-pipelines-1/go")}).
				Exec("dagger call build --src=https://github.com/golang/example#master:/hello --arch=amd64 --os=linux publish --address=ttl.sh/my-img").
				Gif()).
		WithFile(
			"build-export.gif",
			dag.Recorder(dagger.RecorderOpts{Workdir: m.Snippets.Directory("programmable-pipelines-2/go")}).
				Exec("cat main.go").
				Exec("dagger call build --src=https://github.com/golang/example#master:/hello --arch=amd64 --os=linux export --path=/tmp/out").
				Gif())
}
