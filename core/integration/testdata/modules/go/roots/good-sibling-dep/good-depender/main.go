package main

type GoodSiblingDep struct{}

func (m *GoodSiblingDep) Hello() string {
	return "hello"
}
