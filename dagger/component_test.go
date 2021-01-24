package dagger

import (
	"context"
	"testing"
)

func TestComponentNotExist(t *testing.T) {
	cc := &Compiler{}
	root, err := cc.Compile("root.cue", `
foo: hello: "world"
`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewComponent(root.Get("bar")) // non-existent key
	if err != ErrNotExist {
		t.Fatal(err)
	}
	_, err = NewComponent(root.Get("foo")) // non-existent #dagger
	if err != ErrNotExist {
		t.Fatal(err)
	}
}

func TestLoadEmptyComponent(t *testing.T) {
	cc := &Compiler{}
	root, err := cc.Compile("root.cue", `
foo: #dagger: {}
`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewComponent(root.Get("foo"))
	if err != nil {
		t.Fatal(err)
	}
}

// Test that default values in spec are applied at the component level
// See issue #19
func TestComponentDefaults(t *testing.T) {
	t.Skip("FIXME: issue #19")
	cc := &Compiler{}
	v, err := cc.Compile("", `
#dagger: compute: [
	{
        do: "fetch-container"
        ref: "busybox"
	},
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
	c, err := NewComponent(v)
	if err != nil {
		t.Fatal(err)
	}
	// Issue #19 is triggered by:
	// 1. Compile component
	// 2. Get compute script from component
	// 3. Walk script
	s, err := c.ComputeScript()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Walk(context.TODO(), func(op *Op) error {
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateEmptyComponent(t *testing.T) {
	cc := &Compiler{}
	v, err := cc.Compile("", "#dagger: compute: _")
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewComponent(v)
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
	c, err := NewComponent(v)
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
