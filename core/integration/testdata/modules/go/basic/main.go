package main

import (
	"context"
	"fmt"
)

type Basic struct{}

type InputOpt struct {
	Foo string
	Bar []int
}

func (m *Basic) MyFunction(ctx context.Context, stringArg string, intsArg []int, opt *InputOpt) (*Container, error) {
	return dag.Container().From("alpine:latest").WithExec([]string{"echo",
		stringArg,
		fmt.Sprintf("%+v", intsArg),
		fmt.Sprintf("%+v", opt),
	}).Sync(ctx)
}

func (m *Basic) GetCustomObj(ctx context.Context, stringArg string) (*CustomObj, error) {
	return &CustomObj{CustomObjField: stringArg}, nil
}

type CustomObj struct {
	CustomObjField string
}

func (obj *CustomObj) SayField(ctx context.Context) (string, error) {
	return "look: " + obj.CustomObjField, nil
}

/* TODO: doesn't work yet
func (obj *CustomObj) WithField(ctx context.Context, f string) (*CustomObj, error) {
	obj.CustomObjField = f
	return obj, nil
}

func (container *Container) Blah(ctx context.Context, val string) (string, error) {
	return container.WithExec([]string{"echo", val}).Stdout(ctx)
}

func (container *Container) WithCustomEnv(ctx context.Context, val string) (*Container, error) {
	return container.WithEnvVariable("CUSTOM_ENV", val), nil
}
*/
