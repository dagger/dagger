package main

import (
	"dagger/my-module/internal/dagger"
)

type MyModule struct{}

func (m *MyModule) Agent() *dagger.Directory {
	dir := dag.Git("github.com/dagger/dagger").Branch("main").Tree()
	environment := dag.Env().
		WithDirectoryInput("source", dir, "the source directory to use").
		WithDirectoryOutput("result", "the updated directory")

	work := dag.LLM().
		WithEnv(environment).
		WithPrompt(`
			You have access to a directory containing various files.
			Translate only the README file in the directory to French and Spanish.
			Ensure that the translations are accurate and maintain the original formatting.
			Do not modify any other files in the directory.
			Create a sub-directory named 'translations' to store the translated files.
			For French, add an 'fr' suffix to the translated file name.
			For Spanish, add an 'es' suffix to the translated file name.
			Do not create any other new files or directories.
			Do not delete any files or directories.
			Do not investigate any sub-directories.
			Once complete, return the 'translations' directory.
			`)

	return work.
		Env().
		Output("result").
		AsDirectory()
}
