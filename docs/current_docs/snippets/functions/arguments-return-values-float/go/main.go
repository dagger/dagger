package main

type MyModule struct{}

func (m *MyModule) AddFloat(a float64, b float64) float64 {
  return a + b
}
