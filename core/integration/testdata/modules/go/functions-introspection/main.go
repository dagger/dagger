package main

import (
	"context"

	"dagger/test/internal/dagger"
)

type Test struct{}

// doc for FnA
func (m *Test) FnA() *dagger.Container {
	return nil
}

// doc for FnB
func (m *Test) FnB() Duck {
	return nil
}

type Duck interface {
	DaggerObject
	// quack that thang
	Quack(ctx context.Context) (string, error)
}

// doc for FnC
func (m *Test) FnC() *Obj {
	return nil
}

// doc for Prim
func (m *Test) Prim() string {
	return "yo"
}

type Obj struct {
	// doc for FieldA
	FieldA *dagger.Container
	// doc for FieldB
	FieldB string
	// doc for FieldC
	FieldC *Obj
	// doc for FieldD
	FieldD *OtherObj
}

// doc for FnD
func (m *Obj) FnD() *dagger.Container {
	return nil
}

type OtherObj struct {
	// doc for OtherFieldA
	OtherFieldA *dagger.Container
	// doc for OtherFieldB
	OtherFieldB string
	// doc for OtherFieldC
	OtherFieldC *Obj
	// doc for OtherFieldD
	OtherFieldD *OtherObj
}

// doc for FnE
func (m *OtherObj) FnE() *dagger.Container {
	return nil
}
