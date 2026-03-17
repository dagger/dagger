package main

import (
	"dagger/my-module/internal/dagger"
)

type MyModule struct{}

func (m *MyModule) Agent() *dagger.File {
	dir := dag.Git("github.com/golang/example").Branch("master").Tree().Directory("/hello")
	builder := dag.Container().From("golang:latest")

	environment := dag.Env().
		WithContainerInput("container", builder, "a Golang container").
		WithDirectoryInput("directory", dir, "a directory with source code").
		WithFileOutput("file", "the built Go executable")

	work := dag.LLM().
		WithEnv(environment).
		WithPrompt(`
			You have access to a Golang container.
			You also have access to a directory containing Go source code.
			Mount the directory into the container and build the Go application.
			Once complete, return only the built binary.
			`)

	return work.
		Env().
		Output("file").
		AsFile()
}
