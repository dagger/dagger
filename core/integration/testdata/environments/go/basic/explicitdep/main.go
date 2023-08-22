package main

import "context"

func main() {
	dag.CurrentEnvironment().
		WithCheck(AnotherCoolStaticCheck).
		WithCheck(AnotherSadStaticCheck).
		WithCheck(AnotherCoolContainerCheck).
		WithCheck(AnotherSadContainerCheck).
		WithCheck(AnotherCoolCompositeCheck).
		WithCheck(AnotherSadCompositeCheck).
		Serve()
}

func AnotherCoolStaticCheck(ctx context.Context) (*CheckResult, error) {
	return dag.StaticCheckResult(true, StaticCheckResultOpts{
		Output: "We're also cool",
	}), nil
}

func AnotherSadStaticCheck(ctx context.Context) (*CheckResult, error) {
	return dag.StaticCheckResult(false, StaticCheckResultOpts{
		Output: "So sad too...",
	}), nil
}

func AnotherCoolContainerCheck(ctx context.Context) (*Check, error) {
	return dag.Check().WithContainer(dag.Container().
		From("alpine:3.18").
		WithEnvVariable("BASICEXPLICITDEP", "1").
		WithExec([]string{"sh", "-c", "echo BASICEXPLICITDEP $BASICEXPLICITDEP; true"}),
	), nil
}

func AnotherSadContainerCheck(ctx context.Context) (*Check, error) {
	return dag.Check().WithContainer(dag.Container().
		From("alpine:3.18").
		WithEnvVariable("BASICEXPLICITDEP", "2").
		WithExec([]string{"sh", "-c", "echo BASICEXPLICITDEP $BASICEXPLICITDEP; false"}),
	), nil
}

func AnotherCoolCompositeCheck(ctx context.Context) (*Check, error) {
	ctr := dag.Container().From("alpine:3.18")
	return dag.Check().
		WithSubcheck(dag.Check().WithName("AnotherCoolSubcheck1").WithContainer(
			ctr.WithEnvVariable("BASICEXPLICITDEP", "1").WithExec([]string{"sh", "-c", "echo BASICEXPLICITDEP $BASICEXPLICITDEP; true"}),
		)).WithSubcheck(dag.Check().WithName("AnotherCoolSubcheck2").WithContainer(
		ctr.WithEnvVariable("BASICEXPLICITDEP", "2").WithExec([]string{"sh", "-c", "echo BASICEXPLICITDEP $BASICEXPLICITDEP; true"}),
	)), nil
}

func AnotherSadCompositeCheck(ctx context.Context) (*Check, error) {
	ctr := dag.Container().From("alpine:3.18")
	return dag.Check().
		WithSubcheck(dag.Check().WithName("AnotherSadSubcheck3").WithContainer(
			ctr.WithEnvVariable("BASICEXPLICITDEP", "3").WithExec([]string{"sh", "-c", "echo BASICEXPLICITDEP $BASICEXPLICITDEP; false"}),
		)).WithSubcheck(dag.Check().WithName("AnotherSadSubcheck4").WithContainer(
		ctr.WithEnvVariable("BASICEXPLICITDEP", "4").WithExec([]string{"sh", "-c", "echo BASICEXPLICITDEP $BASICEXPLICITDEP; false"}),
	)), nil
}
