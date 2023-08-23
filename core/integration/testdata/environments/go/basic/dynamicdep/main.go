package main

import (
	"context"

	"github.com/ettle/strcase"
)

func main() {
	dag.CurrentEnvironment().
		WithCheck(YetAnotherCoolStaticCheck).
		WithCheck(YetAnotherSadStaticCheck).
		WithCheck(YetAnotherCoolContainerCheck).
		WithCheck(YetAnotherSadContainerCheck).
		WithCheck(YetAnotherCoolCompositeCheck).
		WithCheck(YetAnotherSadCompositeCheck).
		Serve()
}

func checkOutput(name string) string {
	return "WE ARE RUNNING CHECK " + strcase.ToKebab(name)
}

func containerCheck(name string, succeed bool) *Check {
	cmd := "false"
	if succeed {
		cmd = "true"
	}
	ctr := dag.Container().From("alpine:3.18").
		WithExec([]string{"sh", "-e", "-c", "echo " + checkOutput(name) + "; " + cmd})
	return dag.Check().WithName(name).WithContainer(ctr)
}

func YetAnotherCoolStaticCheck(ctx context.Context) (*CheckResult, error) {
	return dag.StaticCheckResult(true, StaticCheckResultOpts{
		Output: checkOutput("YetAnotherCoolStaticCheck"),
	}), nil
}

func YetAnotherSadStaticCheck(ctx context.Context) (*CheckResult, error) {
	return dag.StaticCheckResult(false, StaticCheckResultOpts{
		Output: checkOutput("YetAnotherSadStaticCheck"),
	}), nil
}

func YetAnotherCoolContainerCheck(ctx context.Context) (*Check, error) {
	return containerCheck("YetAnotherCoolContainerCheck", true), nil
}

func YetAnotherSadContainerCheck(ctx context.Context) (*Check, error) {
	return containerCheck("YetAnotherSadContainerCheck", false), nil
}

func YetAnotherCoolCompositeCheck(ctx context.Context) (*Check, error) {
	return dag.Check().
		WithSubcheck(containerCheck("YetAnotherCoolSubcheck1", true)).
		WithSubcheck(containerCheck("YetAnotherCoolSubcheck2", true)), nil
}

func YetAnotherSadCompositeCheck(ctx context.Context) (*Check, error) {
	return dag.Check().
		WithSubcheck(containerCheck("YetAnotherSadSubcheck1", false)).
		WithSubcheck(containerCheck("YetAnotherSadSubcheck2", false)), nil
}
