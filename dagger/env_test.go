package dagger

import (
	"context"
	"testing"
)

func TestSimpleEnvSet(t *testing.T) {
	env, err := NewEnv(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.SetInput(`hello: "world"`); err != nil {
		t.Fatal(err)
	}
	hello, err := env.State().Get("hello").String()
	if err != nil {
		t.Fatal(err)
	}
	if hello != "world" {
		t.Fatal(hello)
	}
}

func TestSimpleEnvSetFromInputValue(t *testing.T) {
	env, err := NewEnv(nil)
	if err != nil {
		t.Fatal(err)
	}

	v, err := env.Compiler().Compile("", `hello: "world"`)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.SetInput(v); err != nil {
		t.Fatal(err)
	}
	hello, err := env.State().Get("hello").String()
	if err != nil {
		t.Fatal(err)
	}
	if hello != "world" {
		t.Fatal(hello)
	}
}

func TestEnvInputComponent(t *testing.T) {
	env, err := NewEnv(nil)
	if err != nil {
		t.Fatal(err)
	}

	v, err := env.Compiler().Compile("", `foo: #dagger: compute: [{do:"local",dir:"."}]`)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.SetInput(v); err != nil {
		t.Fatal(err)
	}

	localdirs, err := env.LocalDirs(context.TODO())
	if err != nil {
		t.Fatal(err)
	}
	if len(localdirs) != 1 {
		t.Fatal(localdirs)
	}
	if dir, ok := localdirs["."]; !ok || dir != "." {
		t.Fatal(localdirs)
	}
}
