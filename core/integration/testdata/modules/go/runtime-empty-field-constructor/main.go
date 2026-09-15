package main

import "dagger/test/internal/dagger"

type Test struct {
	A string
	B int
	C *dagger.Container
	// These aren't tested here, since we can't give them zero values in the constructor.
	// D dagger.ImageLayerCompression
	// E dagger.Platform
}

func New() *Test {
	return &Test{}
}
