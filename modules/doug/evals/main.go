package main

import (
	"context"
	"dagger/evals/internal/dagger"
	"dagger/evals/internal/querybuilder"
)

// Doug's eval suite.
type Evals struct{}

func (evals *Evals) Check(ctx context.Context) error {
	return evals.evaluator().
		EvalsAcrossModels(dagger.EvaluatorEvalsAcrossModelsOpts{
			// These evals are pretty heavy, so don't be too parallel.
			Attempts: 3,
			Models: []string{
				// Currently only targeting Claude.
				"claude-sonnet-4-0",
				// Saw this work once. Consider it?
				// "gpt-4.1",
				// Haven't seen this work yet.
				// "gemini-2.5-pro",
			},
		}).
		Check(ctx)
}

func (evals *Evals) Iterate(ctx context.Context) (string, error) {
	return evals.evaluator().Iterate(ctx)
}

func (evals *Evals) evaluator() *dagger.Evaluator {
	return dag.Evaluator().
		WithEvals([]*dagger.EvaluatorEval{
			evals.eval("andOperator"),
		})
}

func (evals *Evals) eval(name string) *dagger.EvaluatorEval {
	eval := (&dagger.EvaluatorEval{})
	eval = eval.WithGraphQLQuery(
		querybuilder.Query().
			Client(dag.GraphQLClient()).
			Select("evals").
			Select(name).
			Select("asEvaluatorEval"))
	return eval
}
