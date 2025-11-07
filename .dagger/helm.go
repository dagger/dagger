package main

import "context"

// Lint the helm chart
// +check
func (dev *DaggerDev) LintHelm(ctx context.Context) error {
	return dag.Helm().Lint(ctx)
}

// Verify that helm works correctly
// +check
func (dev *DaggerDev) TestHelm(ctx context.Context) error {
	return dag.Helm().Test(ctx)
}
