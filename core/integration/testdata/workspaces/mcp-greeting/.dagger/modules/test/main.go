package main

type Test struct{}

func (m *Test) Greeting() string {
	return "hello from module"
}
