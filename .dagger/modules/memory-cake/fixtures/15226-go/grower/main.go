// An object whose state grows with every call, like an agent's tool object.
package main

import "strings"

type Grower struct {
	// The state: text that grows with every call.
	Log string
}

// A copy with n more bytes of text.
func (g *Grower) Grow(n int) *Grower {
	return &Grower{Log: g.Log + strings.Repeat("x", n)}
}

// The state's length.
func (g *Grower) Size() int {
	return len(g.Log)
}
