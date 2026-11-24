package main

import "dagger/probe/internal/dagger"

type Probe struct {
	// +private
	Src *dagger.Directory
}

func New(src *dagger.Directory) *Probe { return &Probe{Src: src} }

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
