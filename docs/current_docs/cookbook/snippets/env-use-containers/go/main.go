package main

import (
	"dagger/my-module/internal/dagger"
)

type MyModule struct{}

func (m *MyModule) Agent() *dagger.Container {
	base := dag.Container().From("alpine:latest")
	environment := dag.Env().
		WithContainerInput("base", base, "a base container to use").
		WithContainerOutput("result", "the updated container")

	work := dag.LLM().
		WithEnv(environment).
		WithPrompt(`
			You are a software engineer with deep knowledge of Web application development.
			You have access to a container.
			Install the necessary tools and libraries to create a
			complete development environment for Web applications.
			Once complete, return the updated container.
			`)

	return work.
		Env().
		Output("result").
		AsContainer()
}
