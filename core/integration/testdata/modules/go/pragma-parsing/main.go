package main

type Test struct{}

func (t *Test) Hello(
	// +optional
	// +default="blah+dagger-ci@dagger.io"
	argWhereDefaultHasAPlusSign string,
) string {
	return argWhereDefaultHasAPlusSign
}
