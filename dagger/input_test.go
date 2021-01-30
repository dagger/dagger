package dagger

import (
	"context"
	"testing"
)

func TestEnvInputFlag(t *testing.T) {
	env, err := NewEnv()
	if err != nil {
		t.Fatal(err)
	}

	input, err := NewInputValue(env.Compiler(), `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if err := input.DirFlag().Set("www.source=."); err != nil {
		t.Fatal(err)
	}
	if err := env.SetInput(input.Value()); err != nil {
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
