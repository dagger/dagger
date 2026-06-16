package main

type Mallard struct{}

func (m *Mallard) Quack() string {
	return "quack"
}

func (m *Mallard) Fly() string {
	return "fly"
}
