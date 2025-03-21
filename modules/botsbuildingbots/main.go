package main

import (
	"context"
	"dagger/botsbuildingbots/internal/dagger"
)

type BotsBuildingBots struct {
}

func (m *BotsBuildingBots) Singularity(
	ctx context.Context,
	// +default=""
	model string,
	// +default=2
	evals int,
) (string, error) {
	return dag.LLM().
		WithPrompt("You are an LLM prompt engineer trying to find the best system prompt for a tool calling scheme.").
		WithPrompt("The tool calling system is sort of like a functional state machine, backed by an immutable GraphQL API.").
		WithPrompt("The available tools are determined by the currently selected GraphQL Object.").
		WithPrompt("Additional Objects are assigned as variables like `$mounted_ctr`, which each have a `_select_mounted_ctr` tool for selecting that Object. You can also ave the current Object to a variable with the _save tool.").
		WithPrompt("When a field returns an Object type, it becomes the selected Object, replacing the set of tools.").
		WithPrompt("When a tool accepts an Object ID type as an argument, you must pass it as a variable.").
		WithPrompt("I have given you a starting point. Your task is to find the best system prompt.").
		WithPrompt("Focus on framing - once you find a good framing, the prompt shouldn't need to be too long.").
		WithPrompt("After each evaluation, analyze the success rate and history and generate a report. If 100% of the attempts succeeded, you may stop. If not, explain your thought process for the next iteration.").
		WithPrompt("Keep going until you find the best prompt.").
		WithWorkspace(dag.Workspace(dagger.WorkspaceOpts{
			Model: model,
			Evals: evals,
		})).
		Workspace().
		SystemPrompt(ctx)
}
