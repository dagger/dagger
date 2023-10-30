package main

type Test struct{}

func (m *Test) Fn() *CustomObject {
	return &CustomObject{ID: "NOOOO!!!!"}
}

type CustomObject struct {
	ID string `json:"id"`
}
