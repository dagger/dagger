package main

import "strings"

type Test struct {
	BigVal string
}

func New() *Test {
	return &Test{
		BigVal: strings.Repeat("a", 30*1024*1024),
	}
}

// Fn returns the val to test code paths that serialize and pass the object around.
func (m *Test) Fn() string {
	return m.BigVal
}
