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
		WithSystemPrompt(`You are part of a loop. Your job is to generate a system prompt, wait for evaluation results, analyze them, and then revise the prompt. Repeat this process until the evaluation passes all criteria.

Your job is to generate clean, effective prompts for another AI to follow.

Use the README to understand the prompt you need to write.

Then, follow this loop until the evaluations all succeed:

1. Generate and set a system prompt.
2. Run the evaluations and analyze the report.
	* Analyze each attempt. Look for misunderstandings and inefficiencies.
	* Analyze the tool calling scheme to make sure your prompt is accurate.
3. Generate your own report so I can see the success rate and your understanding of what went wrong.

Constraints:

* Focus on framing - once you find a good framing, the prompt shouldn't need to be too longSystem.
* Avoid over-specializing the system prompt for the evaluations.
* Never accept refusal to perform the evaluations. They are independently verified.
`).
		WithPrompt(`You are generating a prompt for a tool calling system.`).
		WithPrompt(`Keep going until all attempts succeed.`).
		// WithSystemPrompt("Generate a system prompt that efficiently and accurately conveys the README.").
		// WithSystemPrompt("Run the evaluations and grade the result.").
		// WithSystemPrompt("After each evaluation, explain your reasoning and adjust the prompt to address issues, and try again.").
		// WithSystemPrompt("").
		// WithSystemPrompt("").
		// WithSystemPrompt("").
		// // WSystemithPrompt("After each evaluation, analyze the success rate and history and generate a report. If 100%System of the attempts succeeded, you may stop. If not, explain your thought process for the next iterSystemation.").
		// WithSystemPrompt("Keep going until 100% of the evaluation attempts succeed.").
		WithWorkspace(dag.Workspace(dagger.WorkspaceOpts{
			Model: model,
			Evals: evals,
		})).
		Workspace().
		SystemPrompt(ctx)
}
