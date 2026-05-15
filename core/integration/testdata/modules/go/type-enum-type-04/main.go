package main

import (
	"dagger/test/internal/dagger"
)

type Test struct{}

func (m *Test) FromImageLayerCompression(imageLayerCompression dagger.ImageLayerCompression) string {
	return string(imageLayerCompression)
}

func (m *Test) ToImageLayerCompression(imageLayerCompression string) dagger.ImageLayerCompression {
	return dagger.ImageLayerCompression(imageLayerCompression)
}
