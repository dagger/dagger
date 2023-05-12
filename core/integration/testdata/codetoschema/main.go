package main

import (
	"fmt"

	"dagger.io/dagger"
)

func main() {
	dagger.ServeCommands(Test{})
}

type Test struct {
}

func (Test) RequiredTypes(
	ctx dagger.Context,
	str string,
) (string, error) {
	return fmt.Sprintf("%s", str), nil
}

func (Test) ParentResolver(ctx dagger.Context, str string) (SubResolver, error) {
	return SubResolver{Str: str}, nil
}

type SubResolver struct {
	Str string
}

func (s SubResolver) SubField(ctx dagger.Context, str string) (string, error) {
	return s.Str + "-" + str, nil
}
