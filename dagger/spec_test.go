package dagger

import (
	"testing"

	"cuelang.org/go/cue"
)

func TestMatch(t *testing.T) {
	var data = []struct {
		Src string
		Def string
	}{
		{
			Src: `do: "exec", args: ["echo", "hello"]`,
			Def: "#Exec",
		},
		{
			Src: `do: "fetch-git", remote: "github.com/shykes/tests"`,
			Def: "#FetchGit",
		},
		{
			Src: `do: "load", from: [{do: "exec", args: ["echo", "hello"]}]`,
			Def: "#Load",
		},
		{
			Src: `do: "load", from: #dagger: compute: [{do: "exec", args: ["echo", "hello"]}]`,
			Def: "#Load",
		},
		// Make sure an empty op does NOT match
		{
			Src: ``,
			Def: "",
		},
		{
			Src: `do: "load"
let package={bash:">3.0"}
from: foo
let foo={#dagger: compute: [
	{do: "fetch-container", ref: "alpine"},
	for pkg, info in package {
		if (info & true) != _|_ {
			do: "exec"
			args: ["echo", "hello", pkg]
		}
		if (info & string) != _|_ {
			do: "exec"
			args: ["echo", "hello", pkg, info]
		}
	}
]}
`,
			Def: "#Load",
		},
	}
	for _, d := range data {
		testMatch(t, d.Src, d.Def)
	}
}

// Test an example op for false positives and negatives
func testMatch(t *testing.T, src interface{}, def string) {
	r := &Runtime{}
	op := compile(t, r, src)
	if def != "" {
		if !r.matchSpec(op, def) {
			t.Errorf("false negative: %s: %q", def, src)
		}
	}
	for _, cmpDef := range []string{
		"#Exec",
		"#FetchGit",
		"#FetchContainer",
		"#Export",
		"#Load",
		"#Copy",
	} {
		if cmpDef == def {
			continue
		}
		if r.matchSpec(op, cmpDef) {
			t.Errorf("false positive: %s: %q", cmpDef, src)
		}
	}
	return
}

func compile(t *testing.T, r *Runtime, src interface{}) cue.Value {
	inst, err := r.Compile("", src)
	if err != nil {
		t.Fatal(err)
	}
	return inst.Value()
}
