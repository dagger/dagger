package dagger

import (
	"context"
	"testing"

	"dagger.cloud/go/dagger/cc"
)

// Test that a script with missing fields DOES NOT cause an error
// NOTE: this behavior may change in the future.
func TestScriptMissingFields(t *testing.T) {
	s, err := CompileScript("test.cue", `
		[
			{
				do: "fetch-container"
				// Missing ref, should cause an error
			}
		]
	`)
	if err != nil {
		t.Fatalf("err=%v\nval=%v\n", err, s.Value().Cue())
	}
}

// Test that a script with defined, but unfinished fields is ignored.
func TestScriptUnfinishedField(t *testing.T) {
	// nOps=1 to make sure only 1 op is counted
	mkScript(t, 1, `
		[
			{
				do: "fetch-container"
				// Unfinished op: should ignore subsequent ops.
				ref: string
			},
			{
				do: "exec"
				args: ["echo", "hello"]
			}
		]
	`)
}

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
	// Walk triggers issue #19 UNLESS optional fields removed from spec.cue
	if err := op.Walk(context.TODO(), func(op *Op) error {
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateEmptyValue(t *testing.T) {
	v, err := cc.Compile("", "#dagger: compute: _")
	if err != nil {
		t.Fatal(err)
	}
	if err := spec.Validate(v.Get("#dagger.compute"), "#Script"); err != nil {
		t.Fatal(err)
	}
}

func TestLocalScript(t *testing.T) {
	ctx := context.TODO()

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

func TestWalkBiggerScript(t *testing.T) {
	t.Skip("FIXME")

	ctx := context.TODO()
	script, err := CompileScript("boot.cue", `
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
	wanted := map[string]string{
		"ga":  "ga",
		"bu":  "bu",
		"zo":  "zo",
		"meu": "meu",
	}
	if len(wanted) != len(dirs) {
		t.Fatal(dirs)
	}
	for k, wantedV := range wanted {
		gotV, ok := dirs[k]
		if !ok {
			t.Fatal(dirs)
		}
		if gotV != wantedV {
			t.Fatal(dirs)
		}
	}
}

// UTILITIES

// Compile a script and check that it has the correct
// number of operations.
func mkScript(t *testing.T, nOps int, src string) *Script {
	s, err := CompileScript("test.cue", src)
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
