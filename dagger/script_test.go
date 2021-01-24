package dagger

import (
	"context"
	"strings"
	"testing"
)

// Test a script which loads a nested script
func TestScriptLoadScript(t *testing.T) {
	mkScript(t, 2, `
	[
		{
			do: "load"
			from: [
				{
					do: "fetch-container"
					ref: "alpine:latest"
				}
			]
		}
	]
	`)
}

// Test a script which loads a nested component
func TestScriptLoadComponent(t *testing.T) {
	mkScript(t, 2, `
[
	{
		do: "load"
		from: {
			#dagger: compute: [
				{
					do: "fetch-container"
					ref: "alpine:latest"
				}
			]
		}
	}
]
`)
}

// Test that default values in spec are applied
func TestScriptDefaults(t *testing.T) {
	cc := &Compiler{}
	v, err := cc.Compile("", `
	[
    {
        do: "exec"
        args: ["sh", "-c", """
            echo hello > /tmp/out
        """]
//      dir: "/"
    }
	]
`)
	if err != nil {
		t.Fatal(err)
	}
	script, err := NewScript(v)
	if err != nil {
		t.Fatal(err)
	}
	op, err := script.Op(0)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := op.Get("dir").String()
	if err != nil {
		t.Fatal(err)
	}
	if dir != "/" {
		t.Fatal(dir)
	}
	t.Skip("FIXME: issue #19")
	// Walk triggers issue #19 UNLESS optional fields removed from spec.cue
	if err := op.Walk(context.TODO(), func(op *Op) error {
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateEmptyValue(t *testing.T) {
	cc := &Compiler{}
	v, err := cc.Compile("", "#dagger: compute: _")
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Get("#dagger.compute").Validate("#Script"); err != nil {
		t.Fatal(err)
	}
}

func TestLocalScript(t *testing.T) {
	ctx := context.TODO()

	cc := &Compiler{}
	src := `[{do: "local", dir: "foo"}]`
	v, err := cc.Compile("", src)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewScript(v)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	err = s.Walk(ctx, func(op *Op) error {
		n++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatal(n)
	}
}

func TestWalkBootScript(t *testing.T) {
	ctx := context.TODO()

	cfg := &ClientConfig{}
	_, err := cfg.Finalize(context.TODO())
	if err != nil {
		t.Fatal(err)
	}

	cc := &Compiler{}
	script, err := cc.CompileScript("boot.cue", cfg.Boot)
	if err != nil {
		t.Fatal(err)
	}
	dirs, err := script.LocalDirs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 1 {
		t.Fatal(dirs)
	}
	if dirs[0] != "." {
		t.Fatal(dirs)
	}
}

func TestWalkBiggerScript(t *testing.T) {
	t.Skip("FIXME")

	ctx := context.TODO()
	cc := &Compiler{}
	script, err := cc.CompileScript("boot.cue", `
[
//	{
//		do: "load"
//		from: {
//			do: "local"
//			dir: "ga"
//		}
//	},
	{
		do: "local"
		dir: "bu"
	},
	{
		do: "copy"
		from: [
			{
				do: "local"
				dir: "zo"
			}
		]
	},
	{
		do: "exec"
		args: ["ls"]
		mount: "/mnt": input: [
			{
				do: "local"
				dir: "meu"
			}
		]
	}
]
`)
	if err != nil {
		t.Fatal(err)
	}
	dirs, err := script.LocalDirs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 4 {
		t.Fatal(dirs)
	}
	wanted := "ga bu zo meu"
	got := strings.Join(dirs, " ")
	if wanted != got {
		t.Fatal(got)
	}
}

// UTILITIES

// Compile a script and check that it has the correct
// number of operations.
func mkScript(t *testing.T, nOps int, src string) *Script {
	cc := &Compiler{}
	s, err := cc.CompileScript("test.cue", src)
	if err != nil {
		t.Fatal(err)
	}
	// Count ops (including nested `from`)
	n := 0
	err = s.Walk(context.TODO(), func(op *Op) error {
		n++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != nOps {
		t.Fatal(n)
	}
	return s
}
