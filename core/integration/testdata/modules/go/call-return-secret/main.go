package main

import "dagger/test/internal/dagger"

type Test struct{}

func (*Test) Secret() *dagger.Secret {
	return dag.SetSecret("foo", "bar")
}

func (*Test) Secret2() *dagger.Secret {
	return dag.SetSecret("fizz", "buzz")
}

func (m *Test) Secrets() []*dagger.Secret {
	return []*dagger.Secret{m.Secret(), m.Secret2()}
}
