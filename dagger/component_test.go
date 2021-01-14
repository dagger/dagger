package dagger

import (
	"context"
	"testing"
)

func TestValidateEmptyComponent(t *testing.T) {
	cc := &Compiler{}
	v, err := cc.Compile("", "#dagger: compute: _")
	if err != nil {
		t.Fatal(err)
	}
	_, err = v.Component()
	if err != nil {
		t.Fatal(err)
	}
}

func TestValidateSimpleComponent(t *testing.T) {
	cc := &Compiler{}
	v, err := cc.Compile("", `hello: "world", #dagger: { compute: [{do:"local",dir:"foo"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	c, err := v.Component()
	if err != nil {
		t.Fatal(err)
	}
	s, err := c.ComputeScript()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	if err := s.Walk(context.TODO(), func(op *Op) error {
		n++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatal(s.v)
	}
}
