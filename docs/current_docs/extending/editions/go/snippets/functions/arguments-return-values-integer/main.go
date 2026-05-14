package main

type MyModule struct{}

func (m *MyModule) AddInteger(a int, b int) int {
  return a + b
}
