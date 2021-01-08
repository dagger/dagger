package dagger

import (
	"testing"
)

func TestLocalMatch(t *testing.T) {
	cc := &Compiler{}
	src := `do: "local", dir: "foo"`
	v, err := cc.Compile("", src)
	if err != nil {
		t.Fatal(err)
	}
	op, err := v.Op()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	err = op.Walk(func(op *Op) error {
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

func TestCopyMatch(t *testing.T) {
	cc := &Compiler{}
	src := `do: "copy", from: [{do: "local", dir: "foo"}]`
	v, err := cc.Compile("", src)
	if err != nil {
		t.Fatal(err)
	}
	op, err := v.Op()
	if err != nil {
		t.Fatal(err)
	}
	if err := op.Validate("#Copy"); err != nil {
		t.Fatal(err)
	}
	n := 0
	err = op.Walk(func(op *Op) error {
		n++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatal(n)
	}
}
