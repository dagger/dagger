package main

import (
	"dagger/minimal/internal/dagger"
	"fmt"
)

type Minimal struct {
	Src  *dagger.Directory
	Name *string
}

func New() *Minimal {
	return &Minimal{}
}

func (m *Minimal) IsEmpty() bool {
	if m.Name != nil {
		panic(fmt.Sprintf("name should be nil but is %v", m.Name))
	}
	if m.Src != nil {
		panic(fmt.Sprintf("src should be nil but is %v", m.Src))
	}
	return true
}
