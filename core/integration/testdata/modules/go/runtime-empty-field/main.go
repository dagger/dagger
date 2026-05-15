package main

import "dagger/test/internal/dagger"

type Test struct {
	A string
	B int
	C *dagger.Container
	D dagger.ImageLayerCompression
	E dagger.Platform
}
