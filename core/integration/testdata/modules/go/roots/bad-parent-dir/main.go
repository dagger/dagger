package main

type BadSubDir struct{}

func (m *BadSubDir) Hello() string {
	return "hello"
}
