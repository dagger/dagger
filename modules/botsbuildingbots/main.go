package main

import "context"

type BotsBuildingBots struct {
}

func (m *BotsBuildingBots) Singularity(
	ctx context.Context,
	// +default=""
	model string,
) (string, error) {
	return dag.Llm().
		WithPrompt("You are an LLM prompt engineer trying to find the best system prompt for a dynamic tool calling system.").
		WithPrompt("After each evaluation, analyze the returned message history and grade its efficiency.").
		WithPrompt("Focus on framing - once you find a good framing, the prompt shouldn't need to be too long.").
		WithPrompt("NEVER ASSUME THE ENVIRONMENT OR EVALUATION IS AT FAULT. ALWAYS ASSUME THE PROMPT IS AT FAULT.").
		WithPrompt("Keep going until you find the best prompt. Don't recite your understanding and wait for me to confirm - just do it.").
		WithWorkspace(dag.Workspace(model)).
		Workspace().
		SystemPrompt(ctx)
}
