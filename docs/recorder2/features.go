package main

import "dagger/recorder/internal/dagger"

// Render feature recordings.
func (r *Recorder) Features() *Features {
	return &Features{
		Recorder: r,
	}
}

type Features struct {
	// +private
	Recorder *Recorder
}

func (f *Features) All() *dagger.Directory {
	// TODO: add secrets
	return dag.Directory().
		WithDirectory("", f.Build()).
		WithDirectory("", f.BuildPublish()).
		WithDirectory("", f.BuildExport()).
		WithDirectory("", f.ShellCurl()).
		WithDirectory("", f.ShellBuild()).
		WithDirectory("", f.ShellHelp()).
		WithDirectory("", f.Tui())
}

func (f *Features) Build() *dagger.Directory {
	return f.buildAndPublish("features/build.tape")
}

func (f *Features) BuildPublish() *dagger.Directory {
	return f.buildAndPublish("features/build-publish.tape")
}

func (f *Features) buildAndPublish(tape string) *dagger.Directory {
	source := dag.CurrentModule().Source().
		Directory("tapes").
		Filter(includeWithDefaults(tape)).
		WithDirectory("", f.Recorder.Source.Directory("docs/current_docs/features/snippets/programmable-pipelines-1/go"))

	return f.Recorder.Vhs.WithSource(source).Render(tape)
}

func (f *Features) BuildExport() *dagger.Directory {
	const tape = "features/build-export.tape"
	source := dag.CurrentModule().Source().
		Directory("tapes").
		Filter(includeWithDefaults(tape)).
		WithDirectory("", f.Recorder.Source.Directory("docs/current_docs/features/snippets/programmable-pipelines-2/go"))

	return f.Recorder.Vhs.WithSource(source).Render(tape)
}

func (f *Features) ShellCurl() *dagger.Directory {
	return f.shell("features/shell-curl.tape")
}

func (f *Features) ShellBuild() *dagger.Directory {
	return f.shell("features/shell-build.tape")
}

func (f *Features) ShellHelp() *dagger.Directory {
	return f.shell("features/shell-help.tape")
}

func (f *Features) shell(tape string) *dagger.Directory {
	return f.Recorder.filteredVhs(includeWithShell(tape)).Render(tape)
}

func (f *Features) Tui() *dagger.Directory {
	const tape = "features/tui.tape"

	return f.Recorder.filteredVhs(includeWithDefaults(tape)).Render(tape)
}
