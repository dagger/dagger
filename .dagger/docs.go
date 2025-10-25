package main

import "context"

// Lint the documentation
func (dev *DaggerDev) LintDocs(ctx context.Context) (CheckStatus, error) {
	_, err := dag.Docs().Lint(ctx)
	return CheckCompleted, err
}
