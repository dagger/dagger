package main

import (
	"context"
	"fmt"

	"dagger/basic/internal/dagger"
)

type Basic struct{}

type InputOpt struct {
	Foo string
	Bar []int
}

func (m *Basic) MyFunction(ctx context.Context, stringArg string, intsArg []int, opt *InputOpt) (*dagger.Container, error) {
	return dag.Container().From("alpine:latest").WithExec([]string{"echo",
		stringArg,
		fmt.Sprintf("%+v", intsArg),
		fmt.Sprintf("%+v", opt),
	}).Sync(ctx)
}

func (m *Basic) CatFile(ctx context.Context, ctr *dagger.Container, f *dagger.File) (string, error) {
	return ctr.WithMountedFile("/foo", f).WithExec([]string{"cat", "/foo"}).Stdout(ctx)
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

func (container *dagger.Container) Blah(ctx context.Context, val string) (string, error) {
	return container.WithExec([]string{"echo", val}).Stdout(ctx)
}

func (obj *CustomObj) WithField(ctx context.Context, f string) (*CustomObj, error) {
	obj.CustomObjField = f
	return obj, nil
}

func (container *dagger.Container) WithCustomEnv(ctx context.Context, val string) (*dagger.Container, error) {
	return container.WithEnvVariable("CUSTOM_ENV", val), nil
}
