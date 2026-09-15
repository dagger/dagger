package main

type Test struct {
	Data string
}

func (m *Test) Set(data string) *Test {
	m.Data = data
	return m
}

func (m *Test) Get() string {
	return m.Data
}
