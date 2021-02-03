package dagger

import (
	"testing"
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
	}
	for _, d := range data {
		testMatch(t, d.Src, d.Def)
	}
}

// Test an example op for false positives and negatives
func testMatch(t *testing.T, src interface{}, def string) {
	cc := &Compiler{}
	op := compile(t, cc, src)
	if def != "" {
		if err := op.Validate(def); err != nil {
			t.Errorf("false negative: %s: %q: %s", def, src, err)
		}
	}
	for _, cmpDef := range []string{
		"#Exec",
		"#FetchGit",
		"#FetchContainer",
		"#Export",
		"#Copy",
		"#Local",
	} {
		if cmpDef == def {
			continue
		}
		if err := op.Validate(cmpDef); err == nil {
			t.Errorf("false positive: %s: %q", cmpDef, src)
		}
	}
}

func compile(t *testing.T, cc *Compiler, src interface{}) *Value {
	v, err := cc.Compile("", src)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
