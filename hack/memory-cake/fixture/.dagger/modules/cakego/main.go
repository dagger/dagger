// Cakego is the memory cake's Go tool module: an object whose state grows
// with every call, as an agent's Go-SDK tools do.
package main

import (
	"fmt"
	"strings"
)

type Cakego struct {
	Items []string
}

// Add appends an item to the object's state.
func (m *Cakego) Add(n int) *Cakego {
	m.Items = append(m.Items, fmt.Sprintf("go item %d %s", n, strings.Repeat("x", 256)))
	return m
}

// Count is how many items the object holds.
func (m *Cakego) Count() int {
	return len(m.Items)
}
