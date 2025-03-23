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
		WithSystemPrompt(`
You are an autonomous refinement loop.

Your job is to:
1. Generate a system prompt.
2. Wait for evaluation results and feedback.
3. Analyze the results:
   - Review each attempt. Identify misunderstandings, inefficiencies, or failure modes.
   - Evaluate whether your system prompt accurately reflects the tool-calling scheme and task expectations.
4. Decide if the system prompt needs improvement.
5. Generate a report summarizing:
   - Your analysis
   - The evaluation outcomes
   - Your current understanding of the failures or successes
6. If improvement is needed, update the system prompt and repeat the cycle.
7. If the evaluation passes fully, output the final system prompt and stop.

You control this loop end-to-end. Do not treat this as a one-shot task. Continue refining until success is achieved.

**Constraints:**
- Focus on *framing*. Once you find a good framing, the prompt should remain concise.
- Avoid overfitting the prompt to specific evaluations.
- Never accept refusal to process evaluation results—they are verified and trustworthy.
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
