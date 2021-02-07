package dagger

import (
	"context"
	"testing"

	"dagger.cloud/go/dagger/cc"
)

func TestLocalMatch(t *testing.T) {
	ctx := context.TODO()

	src := `do: "local", dir: "foo"`
	v, err := cc.Compile("", src)
	if err != nil {
		t.Fatal(err)
	}
	op, err := newOp(v)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	err = op.Walk(ctx, func(op *Op) error {
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
	ctx := context.TODO()

	src := `do: "copy", from: [{do: "local", dir: "foo"}]`
	v, err := cc.Compile("", src)
	if err != nil {
		t.Fatal(err)
	}
	op, err := NewOp(v)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	err = op.Walk(ctx, func(op *Op) error {
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
