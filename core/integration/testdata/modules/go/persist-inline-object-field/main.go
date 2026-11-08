package main

import (
	"crypto/rand"
	"strings"

	"dagger/test/internal/dagger"
)

type Test struct{}

// Outer is cached across sessions by default function caching; the seed arg
// gives each test run a distinct cache identity, and the tokens show whether
// a later call reused it.
func (*Test) Outer(seed string) *Outer {
	return &Outer{
		Inner:  newInner(seed, "inner"),
		Inners: []*Inner{newInner(seed, "first"), newInner(seed, "second")},
	}
}

func newInner(seed, name string) *Inner {
	return &Inner{
		Dir:   dag.Directory().WithNewFile(name, seed),
		Token: rand.Text(),
	}
}

// Outer keeps its Inner objects in its own fields, so their directories are
// references in Outer's state.
type Outer struct {
	Inner  *Inner
	Inners []*Inner
}

func (o *Outer) Tokens() string {
	tokens := []string{o.Inner.Token}
	for _, inner := range o.Inners {
		tokens = append(tokens, inner.Token)
	}
	return strings.Join(tokens, ",")
}

type Inner struct {
	Dir   *dagger.Directory
	Token string
}
