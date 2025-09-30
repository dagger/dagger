package main

import (
	"context"
	"dagger/evals/internal/dagger"
	"dagger/evals/internal/querybuilder"
)

// Models smart enough to follow instructions like 'do X three times.'
var SmartModels = []string{
	"gpt-4o",
	"gpt-4.1",
	"gemini-2.0-flash",
	"claude-3-5-sonnet-latest",
	"claude-3-7-sonnet-latest",
	"claude-sonnet-4-5",
}

// Dagger's eval suite.
type Evals struct {
	Docs *dagger.File
}

func New(
	// +defaultPath=/core/llm_docs.md
	docs *dagger.File,
) *Evals {
	return &Evals{
		Docs: docs,
	}
}

// Run the Dagger evals across the major model providers.
func (es *Evals) Check(
	ctx context.Context,
	// Run particular evals, or all evals if unspecified.
	// +optional
	evals []string,
	// Run particular models, or all models if unspecified.
	// +optional
	models []string,
) error {
	var evaluatorEvals []*dagger.EvaluatorEval
	for _, eval := range []string{
		"basic",
		"buildMulti",
		"buildMultiNoVar",
		"workspacePattern",
		"readImplicitVars",
		"undoChanges",
		"coreApi",
		"moduleDependencies",
		"responses",
		"writable",
		"modelContextProtocol",
	} {
		// TODO: replace with self-calls
		evaluatorEvals = append(evaluatorEvals, (&dagger.EvaluatorEval{}).WithGraphQLQuery(
			querybuilder.Query().Client(dag.GraphQLClient()).
				Select("evals").
				Select(eval).
				Select("asEvaluatorEval"),
		))
	}

	return dag.Evaluator().
		WithDocsFile(es.Docs).
		WithEvals(evaluatorEvals).
		EvalsAcrossModels(dagger.EvaluatorEvalsAcrossModelsOpts{
			Evals:  evals,
			Models: models,
		}).
		Check(ctx)
}
