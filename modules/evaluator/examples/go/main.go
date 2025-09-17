// Examples for the Evaluator module

package main

import (
	"context"
	"dagger/examples/internal/dagger"
)

// A dev environment for the Dagger Engine
type Examples struct {
	Source *dagger.Directory
}

func New(
	ctx context.Context,
	// +optional
	// +defaultPath="/"
	source *dagger.Directory,

) (*Examples, error) {
	dev := &Examples{
		Source: source,
	}
	return dev, nil
}

// Run the Dagger evals across the major model providers.
func (dev *Examples) Evaluator_RunMyEvals(
	ctx context.Context,
	// Run particular evals, or all evals if unspecified.
	// +optional
	evals []string,
	// Run particular models, or all models if unspecified.
	// +optional
	models []string,
) error {
	myEvaluator := dag.Evaluator().
		WithDocsFile(dev.Source.File("core/llm_docs.md")).
		WithoutDefaultSystemPrompt().
		WithSystemPromptFile(dev.Source.File("core/llm_dagger_prompt.md")).
		WithEvals([]*dagger.EvaluatorEval{
			// FIXME: ideally this list would live closer to where the evals are
			// defined, but it's not possible for a module to return an interface type
			// https://github.com/dagger/dagger/issues/7582
			dag.Evals().Basic().AsEvaluatorEval(),
			dag.Evals().BuildMulti().AsEvaluatorEval(),
			dag.Evals().BuildMultiNoVar().AsEvaluatorEval(),
			dag.Evals().WorkspacePattern().AsEvaluatorEval(),
			dag.Evals().ReadImplicitVars().AsEvaluatorEval(),
			dag.Evals().UndoChanges().AsEvaluatorEval(),
			dag.Evals().CoreAPI().AsEvaluatorEval(),
			dag.Evals().ModuleDependencies().AsEvaluatorEval(),
			dag.Evals().Responses().AsEvaluatorEval(),
		})
	return myEvaluator.
		EvalsAcrossModels(dagger.EvaluatorEvalsAcrossModelsOpts{
			Evals:  evals,
			Models: models,
		}).
		Check(ctx)
}
