package dagger

import (
	"testing"
)

func TestSimple(t *testing.T) {
	cc := &Compiler{}
	_, err := cc.EmptyStruct()
	if err != nil {
		t.Fatal(err)
	}
}

func TestJSON(t *testing.T) {
	cc := &Compiler{}
	v, err := cc.Compile("", `foo: hello: "world"`)
	if err != nil {
		t.Fatal(err)
	}
	b1 := v.JSON()
	if string(b1) != `{"foo":{"hello":"world"}}` {
		t.Fatal(b1)
	}
	// Reproduce a bug where Value.Get().JSON() ignores Get()
	b2 := v.Get("foo").JSON()
	if string(b2) != `{"hello":"world"}` {
		t.Fatal(b2)
	}
}

func TestCompileSimpleScript(t *testing.T) {
	cc := &Compiler{}
	_, err := cc.CompileScript("simple.cue", `[{do: "local", dir: "."}]`)
	if err != nil {
		t.Fatal(err)
	}
}
