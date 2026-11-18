// Soakgo is the Go half of the memory soak's module calls: an object whose
// state grows with every call, and a directory edit made from that state.
package main

import (
	"fmt"
	"strings"

	"dagger/soakgo/internal/dagger"
)

type Soakgo struct {
	Items []string
}

// Add appends an item to the object's state.
func (m *Soakgo) Add(n int) *Soakgo {
	m.Items = append(m.Items, fmt.Sprintf("go item %d %s", n, strings.Repeat("x", 128)))
	return m
}

// Count is how many items the object holds.
func (m *Soakgo) Count() int {
	return len(m.Items)
}

// Edit writes the object's state into the tree.
func (m *Soakgo) Edit(tree *dagger.Directory, n int) *dagger.Directory {
	return tree.WithNewFile(fmt.Sprintf("go/%d.txt", n), strings.Join(m.Items, "\n"))
}
