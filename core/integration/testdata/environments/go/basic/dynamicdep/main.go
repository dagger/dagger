package main

import "context"

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

func YetAnotherCoolStaticCheck(ctx context.Context) (*CheckResult, error) {
	return dag.StaticCheckResult(true, StaticCheckResultOpts{
		Output: "We're also cool too",
	}), nil
}

func YetAnotherSadStaticCheck(ctx context.Context) (*CheckResult, error) {
	return dag.StaticCheckResult(false, StaticCheckResultOpts{
		Output: "Also so sad too...",
	}), nil
}

func YetAnotherCoolContainerCheck(ctx context.Context) (*Check, error) {
	return dag.Check().WithContainer(dag.Container().
		From("alpine:3.18").
		WithEnvVariable("BASICDYNAMICDEP", "1").
		WithExec([]string{"sh", "-c", "echo BASICDYNAMICDEP $BASICDYNAMICDEP; true"}),
	), nil
}

func YetAnotherSadContainerCheck(ctx context.Context) (*Check, error) {
	return dag.Check().WithContainer(dag.Container().
		From("alpine:3.18").
		WithEnvVariable("BASICDYNAMICDEP", "2").
		WithExec([]string{"sh", "-c", "echo BASICDYNAMICDEP $BASICDYNAMICDEP; false"}),
	), nil
}

func YetAnotherCoolCompositeCheck(ctx context.Context) (*Check, error) {
	ctr := dag.Container().From("alpine:3.18")
	return dag.Check().
		WithSubcheck(dag.Check().WithName("YetAnotherCoolSubcheck1").WithContainer(
			ctr.WithEnvVariable("BASICDYNAMICDEP", "1").WithExec([]string{"sh", "-c", "echo BASICDYNAMICDEP $BASICDYNAMICDEP; true"}),
		)).WithSubcheck(dag.Check().WithName("YetAnotherCoolSubcheck2").WithContainer(
		ctr.WithEnvVariable("BASICDYNAMICDEP", "2").WithExec([]string{"sh", "-c", "echo BASICDYNAMICDEP $BASICDYNAMICDEP; true"}),
	)), nil
}

func YetAnotherSadCompositeCheck(ctx context.Context) (*Check, error) {
	ctr := dag.Container().From("alpine:3.18")
	return dag.Check().
		WithSubcheck(dag.Check().WithName("YetAnotherSadSubcheck3").WithContainer(
			ctr.WithEnvVariable("BASICDYNAMICDEP", "3").WithExec([]string{"sh", "-c", "echo BASICDYNAMICDEP $BASICDYNAMICDEP; false"}),
		)).WithSubcheck(dag.Check().WithName("YetAnotherSadSubcheck4").WithContainer(
		ctr.WithEnvVariable("BASICDYNAMICDEP", "4").WithExec([]string{"sh", "-c", "echo BASICDYNAMICDEP $BASICDYNAMICDEP; false"}),
	)), nil
}
