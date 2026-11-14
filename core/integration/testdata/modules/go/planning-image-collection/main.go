package main

import "dagger/probe/internal/dagger"

type Probe struct {
	// +private
	Base *dagger.Container
}

func New(base *dagger.Container) *Probe { return &Probe{Base: base} }

func (*Probe) Batches() *Batches { return &Batches{Names: []string{"a", "b"}} }

// +collection
type Batches struct {
	// +keys
	Names []string
}

// +get
func (*Batches) Batch(name string) *Batch { return &Batch{Name: name} }

type Batch struct{ Name string }

// +check
func (*Batch) Verify() error { return nil }
