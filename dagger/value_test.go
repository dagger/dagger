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

func TestCompileBootScript(t *testing.T) {
	cc := &Compiler{}
	cfg, err := cc.Compile("boot.cue", defaultBootScript)
	if err != nil {
		t.Fatal(err)
	}
	s, err := cfg.Get("bootscript").Script()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestCompileSimpleScript(t *testing.T) {
	cc := &Compiler{}
	s, err := cc.CompileScript("simple.cue", `[{do: "local", dir: "."}]`)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
}
