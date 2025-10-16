package main

import "context"

// Lint the helm chart
func (dev *DaggerDev) LintHelm(ctx context.Context) (MyCheckStatus, error) {
	_, err := dag.Helm().Lint(ctx)
	return CheckCompleted, err
}

// Verify that helm works correctly
func (dev *DaggerDev) TestHelm(ctx context.Context) (MyCheckStatus, error) {
	_, err := dag.Helm().Test(ctx)
	return CheckCompleted, err
}
