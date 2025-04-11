package main

import "dagger/recorder/internal/dagger"

// Render quickstart recordings.
func (r *Recorder) Quickstart() *Quickstart {
	return &Quickstart{
		Recorder: r,
	}
}

type Quickstart struct {
	// +private
	Recorder *Recorder
}

func (f *Quickstart) All() *dagger.Directory {
	// TODO: add docker
	return dag.Directory().
		WithDirectory("", f.Terminal()).
		WithDirectory("", f.BuildenvTerminal()).
		WithDirectory("", f.BuildService()).
		WithDirectory("", f.Docker()).
		WithDirectory("", f.Publish())
}

// Basics quickstart recordings

func (f *Quickstart) Terminal() *dagger.Directory {
	return f.quickstart("quickstart/basics/terminal.tape")
}

func (f *Quickstart) PublishShell() *dagger.Directory {
	return f.quickstart("quickstart/basics/publish-shell.tape")
}

// CI quickstart recordings

func (f *Quickstart) BuildenvTerminal() *dagger.Directory {
	return f.quickstart("quickstart/ci/buildenv-terminal.tape")
}

func (f *Quickstart) BuildService() *dagger.Directory {
	return f.quickstart("quickstart/ci/build-service.tape")
}

func (f *Quickstart) Publish() *dagger.Directory {
	return f.quickstart("quickstart/ci/publish.tape")
}

func (f *Quickstart) Docker() *dagger.Directory {
	return f.quickstart("quickstart/ci/docker.tape")
}

func (f *Quickstart) quickstart(tape string) *dagger.Directory {
	source := dag.CurrentModule().Source().
		Directory("tapes").
		Filter(includeWithShell(tape)).
		WithDirectory("", f.Recorder.Source.Directory("docs/current_docs/quickstart/ci/snippets/go")).
		WithDirectory("", dag.Git("https://github.com/dagger/hello-dagger", dagger.GitOpts{KeepGitDir: true}).Head().Tree())

	return f.Recorder.Vhs.WithSource(source).Render(tape)
}
