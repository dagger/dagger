package main

import (
	"context"
)

type MyModule struct{}

func (m *MyModule) Agent(
	ctx context.Context,
	question string,
) (string, error) {
	environment := dag.Env().
		WithStringInput("question", question, "the question").
		WithStringOutput("answer", "the answer to the question")

	work := dag.LLM().
		WithEnv(environment).
		WithPrompt(`
			You are an assistant that helps with complex questions.
			You will receive a question and you need to provide a detailed answer.
			Make sure to use the provided context and environment variables.
			Your answer should be clear and concise.
			Your question is: $question
			`)

	return work.
		Env().
		Output("answer").
		AsString(ctx)
}
