package main

import "context"

type Test struct{}

func (*Test) Fn(ctx context.Context, rand string) (string, error) {
	return dag.Container().
		From("alpine:3.20").
		WithExec([]string{"apk", "add", "netcat-openbsd"}).
		WithServiceBinding("echoer", dag.Servicer().EchoSvc()).
		WithEnvVariable("CACHEBUSTER", rand).
		WithExec([]string{"sh", "-c", "echo -n $CACHEBUSTER | nc -N echoer 1234"}).
		Stdout(ctx)
}
