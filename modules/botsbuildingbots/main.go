package main

import (
	"context"
	"dagger/botsbuildingbots/internal/dagger"
)

type BotsBuildingBots struct {
	WriterModel string
	EvalModel   string
	Evals       int
}

func New(
	// +optional
	model string,
	// +optional
	evalModel string,
	// Number of evaluations to run.
	// +default=2
	evals int,
) *BotsBuildingBots {
	return &BotsBuildingBots{
		WriterModel: model,
		EvalModel:   evalModel,
		Evals:       evals,
	}
}

func (m *BotsBuildingBots) llm() *dagger.LLM {
	return dag.LLM(dagger.LLMOpts{Model: m.WriterModel}).
		WithWorkspace(dag.Workspace(dagger.WorkspaceOpts{
			Model: m.EvalModel,
			Evals: m.Evals,
		}))
}

func (m *BotsBuildingBots) Singularity(
	ctx context.Context,
	// The model consuming the system prompt and running evaluations.
	// +default=""
	model string,
) (string, error) {
	return m.llm().
		WithSystemPrompt(`You are an autonomous system prompt refinement loop.

Your job is to:
1. Analyze the README and come up with a way to frame the system prompt.
2. Generate a system prompt. START WITH ONE SENTENCE. Framing is PARAMOUNT.
3. Run the evaluations and analyze the results.
4. Generate a report summarizing:
	- Your current understanding of the failures or successes
  - Your analysis of the success rate and token usage cost
5. If improvement is needed, generate a new system prompt and repeat the cycle.
6. If the evaluation passes fully, output the final system prompt and stop.

You control this loop end-to-end. Do not treat this as a one-shot task. Continue refining until success is achieved.
`).
		WithPrompt(`Read the README and generate the best system prompt for it. Keep going until all attempts succeed.`).
		// WithSystemPrompt("Generate a system prompt that efficiently and accurately conveys the README.").
		// WithSystemPrompt("Run the evaluations and grade the result.").
		// WithSystemPrompt("After each evaluation, explain your reasoning and adjust the prompt to address issues, and try again.").
		// WithSystemPrompt("").
		// WithSystemPrompt("").
		// WithSystemPrompt("").
		// // WSystemithPrompt("After each evaluation, analyze the success rate and history and generate a report. If 100%System of the attempts succeeded, you may stop. If not, explain your thought process for the next iterSystemation.").
		// WithSystemPrompt("Keep going until 100% of the evaluation attempts succeed.").
		Workspace().
		SystemPrompt(ctx)
}
