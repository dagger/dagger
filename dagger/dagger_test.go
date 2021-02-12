package dagger

import (
	"testing"

	"dagger.io/go/dagger/compiler"
)

func TestLocalDirs(t *testing.T) {
	env := mkEnv(t,
		`#compute: [
			{
				do: "local"
				dir: "bar"
			}
		]`,
		`dir: #compute: [
			{
				do: "local"
				dir: "foo"
			}
		]`,
	)
	dirs := env.LocalDirs()
	if len(dirs) != 2 {
		t.Fatal(dirs)
	}
	if _, ok := dirs["foo"]; !ok {
		t.Fatal(dirs)
	}
	if _, ok := dirs["bar"]; !ok {
		t.Fatal(dirs)
	}
}

func mkEnv(t *testing.T, updater, input string) *Env {
	env, err := NewEnv()
	if err != nil {
		t.Fatal(err)
	}
	u, err := compiler.Compile("updater.cue", updater)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.SetUpdater(u); err != nil {
		t.Fatal(err)
	}
	i, err := compiler.Compile("input.cue", input)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.SetInput(i); err != nil {
		t.Fatal(err)
	}
	return env
}
